package pluginhost

import (
	"bytes"
	"context"
	"io"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/audio"
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
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
func TestReviewFirstPositiveReadCannotRebindPredecessor(t *testing.T) {
	var packet bytes.Buffer
	if err := audio.WriteFrame(&packet, audio.Frame{Kind: audio.KindPCM, Stream: 41, Seq: 1, PCM: []byte{1, 2}}); err != nil {
		t.Fatal(err)
	}
	r, w := io.Pipe()
	gate := &reviewReadHandoffGate{ReadCloser: r, remaining: 1, read: make(chan struct{}), release: make(chan struct{})}
	pair := newAudioPair(io.Discard, gate)
	ctxA, cancelA := context.WithCancel(context.Background())
	reviewAttachAudio(pair, ctxA, 41)
	defer cancelA()
	defer r.Close()
	defer w.Close()
	go func() { _, _ = w.Write(packet.Bytes()) }()
	select {
	case <-gate.read:
	case <-time.After(time.Second):
		t.Fatal("first positive read did not arrive")
	}
	cancelA()
	ctxB, cancelB := context.WithTimeout(context.Background(), time.Second)
	defer cancelB()
	b := reviewAttachAudio(pair, ctxB, 42)
	close(gate.release)
	go func() {
		_ = audio.WriteFrame(w, audio.Frame{Kind: audio.KindPCM, Stream: 42, Seq: 1, PCM: []byte{3, 4}})
	}()
	frame, err := b.ReadOutput()
	if err != nil || frame.Stream != 42 {
		t.Fatalf("first-read handoff assigned old frame: stream=%d PCM=%v err=%v; want 42", frame.Stream, frame.PCM, err)
	}
}

// .
// .
// .
// .
// .
// .
// .
// .
// .
func TestReviewNoOwnerAtFirstBytesStaysUnowned(t *testing.T) {
	var packet bytes.Buffer
	if err := audio.WriteFrame(&packet, audio.Frame{Kind: audio.KindPCM, Stream: 41, Seq: 1, PCM: []byte{1, 2}}); err != nil {
		t.Fatal(err)
	}
	r, w := io.Pipe()
	defer r.Close()
	defer w.Close()
	gate := &reviewReadHandoffGate{ReadCloser: r, remaining: 1, read: make(chan struct{}), release: make(chan struct{})}
	pair := newAudioPair(io.Discard, gate)
	go func() { _, _ = w.Write(packet.Bytes()) }()
	select {
	case <-gate.read:
	case <-time.After(time.Second):
		t.Fatal("the frame's first byte did not arrive")
	}
	// .
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	late := reviewAttachAudio(pair, ctx, 42)
	close(gate.release)
	go func() {
		_ = audio.WriteFrame(w, audio.Frame{Kind: audio.KindPCM, Stream: 42, Seq: 1, PCM: []byte{3, 4}})
	}()
	got := make(chan audio.Frame, 1)
	go func() { fr, _ := late.ReadOutput(); got <- fr }()
	select {
	case fr := <-got:
		if fr.Stream != 42 {
			t.Fatalf("a frame that began without a session was acquired by the session that attached during it: stream=%d", fr.Stream)
		}
	case <-time.After(time.Second):
		t.Fatal("the late session stalled behind a frame that is not its own")
	}
	cancel()
	deadline := time.Now().Add(2 * time.Second)
	for pair.stray.Load() != 1 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if n := pair.stray.Load(); n != 1 {
		t.Fatalf("the unowned frame must be counted away when the session that could not own it ends: stray=%d", n)
	}
}
