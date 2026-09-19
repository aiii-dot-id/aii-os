package pluginhost

import (
	"bytes"
	"context"
	"github.com/aiii-dot-id/aii-os/internal/audio"
	"io"
	"testing"
	"time"
)

// .
// .
// .
// .
// .
// .
// .
// .
// .
func reviewAttachAudio(p *audioPair, ctx context.Context, streams ...uint32) *sessionChannel {
	c := newSessionChannel(p, ctx)
	for _, s := range streams {
		if err := p.bind(s, c.taker, "review"); err != nil {
			panic(err)
		}
	}
	return c
}

func TestReviewActiveAudioPreservesBytesAndTailBeyondQueueCapacity(t *testing.T) {
	r, w := io.Pipe()
	defer r.Close()
	defer w.Close()
	p := newAudioPair(io.Discard, r)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c := reviewAttachAudio(p, ctx, 1)
	const count = pairBuffer*3 + 7
	wrote := make(chan error, 1)
	go func() {
		defer w.Close()
		for i := 0; i < count; i++ {
			pcm := bytes.Repeat([]byte{byte(i), byte(i >> 8)}, 240)
			if err := audio.WriteFrame(w, audio.Frame{Kind: audio.KindPCM, Stream: 1, Seq: uint32(i + 1), Start: int64(i * 240), PCM: pcm}); err != nil {
				wrote <- err
				return
			}
		}
		wrote <- audio.WriteFrame(w, audio.Frame{Kind: audio.KindEnd, Stream: 1, Seq: count + 1, Start: count * 240})
	}()
	for auditQueued(p) < pairBuffer && ctx.Err() == nil {
		time.Sleep(time.Millisecond)
	}
	if auditQueued(p) != pairBuffer {
		t.Fatal("fixture did not fill the queue")
	}
	time.Sleep(30 * time.Millisecond)
	for i := 0; i < count; i++ {
		fr, err := c.ReadOutput()
		want := bytes.Repeat([]byte{byte(i), byte(i >> 8)}, 240)
		if err != nil || fr.Seq != uint32(i+1) || fr.Start != int64(i*240) || !bytes.Equal(fr.PCM, want) {
			t.Fatalf("missing speech at frame %d of %d: got seq=%d start=%d bytes=%d err=%v; dropped=%d", i, count, fr.Seq, fr.Start, len(fr.PCM), err, p.stray.Load())
		}
	}
	fr, err := c.ReadOutput()
	if err != nil || fr.Kind != audio.KindEnd || fr.Start != count*240 {
		t.Fatalf("missing complete tail: %+v %v", fr, err)
	}
	if err := <-wrote; err != nil {
		t.Fatal(err)
	}
	if p.stray.Load() != 0 {
		t.Fatalf("active audio counted as stray: %d", p.stray.Load())
	}
}

func TestReviewCancelledBacklogCannotLeakIntoRecovery(t *testing.T) {
	r, w := io.Pipe()
	defer r.Close()
	defer w.Close()
	p := newAudioPair(io.Discard, r)
	first, retire := context.WithCancel(context.Background())
	defer retire()
	_ = reviewAttachAudio(p, first, 41)
	for i := 0; i < 8; i++ {
		if err := audio.WriteFrame(w, audio.Frame{Kind: audio.KindPCM, Stream: 41, Seq: uint32(i + 1), PCM: []byte{1, 2}}); err != nil {
			t.Fatal(err)
		}
	}
	deadline := time.Now().Add(time.Second)
	for auditQueued(p) != 8 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if auditQueued(p) != 8 {
		t.Fatal("fixture failed to retain old backlog")
	}
	retire()
	fresh, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	c := reviewAttachAudio(p, fresh, 42)
	if err := audio.WriteFrame(w, audio.Frame{Kind: audio.KindPCM, Stream: 42, Seq: 1, PCM: []byte{3, 4}}); err != nil {
		t.Fatal(err)
	}
	fr, err := c.ReadOutput()
	if err != nil || fr.Stream != 42 || !bytes.Equal(fr.PCM, []byte{3, 4}) {
		t.Fatalf("cancelled speech revived in a fresh session: stream=%d PCM=%v err=%v; want recovery stream 42", fr.Stream, fr.PCM, err)
	}
}
