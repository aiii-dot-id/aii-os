package pluginhost

import (
	"bytes"
	"errors"
	"io"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/audio"
)

// .
// .
// .
type auditAudioReadBarrier struct {
	io.ReadCloser
	reads   int
	entered chan struct{}
	resume  chan struct{}
	once    sync.Once
}

func (r *auditAudioReadBarrier) Read(b []byte) (int, error) {
	r.reads++
	if r.reads == 2 {
		close(r.entered)
		<-r.resume
	}
	return r.ReadCloser.Read(b)
}
func (r *auditAudioReadBarrier) release()     { r.once.Do(func() { close(r.resume) }) }
func (r *auditAudioReadBarrier) Close() error { r.release(); return r.ReadCloser.Close() }

func TestAuditInFlightPredecessorFrameCannotReachSuccessor(t *testing.T) {
	h := auditNewAudioHarness(t)
	// .
	previousPair := h.v.pair
	_ = previousPair.out.Close()
	_ = h.audioOut.Close()
	auditAudioJoin(t, "initial unused pair", previousPair.ended)
	rd, wr, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	gate := &auditAudioReadBarrier{ReadCloser: rd, entered: make(chan struct{}), resume: make(chan struct{})}
	h.v.pair = newAudioPair(io.Discard, gate)
	t.Cleanup(func() { _ = gate.Close(); _ = wr.Close() })
	oldPump := h.open(t, "old", audio.NewCaptureSink(mono16k))
	h.name("old", 71)
	old := audio.Frame{Kind: audio.KindPCM, Stream: 71, Seq: 7, PCM: bytes.Repeat([]byte{0x34, 0x12}, 240)}
	// .
	if err := audio.WriteFrame(wr, old); err != nil {
		t.Fatal(err)
	}
	auditAudioJoin(t, "old header consumed; payload read held", gate.entered)
	h.terminal(t, "old")
	auditAudioJoin(t, "old pump fully ended", oldPump.Done())
	fresh := audio.NewCaptureSink(mono16k)
	h.open(t, "new", fresh)
	h.name("new", 72)
	gate.release()
	if err := audio.WriteFrame(wr, audio.Frame{Kind: audio.KindPCM, Stream: 72, Seq: 99, PCM: []byte{1, 2}}); err != nil {
		t.Fatal(err)
	}
	auditAudioWait(t, "new frame reaches new speaker", func() bool {
		for _, fr := range fresh.Frames() {
			if fr.Stream == 72 && fr.Seq == 99 {
				return true
			}
		}
		return false
	})
	for _, fr := range fresh.Frames() {
		if fr.Stream == 71 {
			t.Fatalf("successor played predecessor frame fully written before terminal: seq=%d PCM_bytes=%d; old pump had fully joined", fr.Seq, len(fr.PCM))
		}
	}
}

// .
// .
func TestAuditBrokenOutputFaultsOwningVoiceSession(t *testing.T) {
	h := auditNewAudioHarness(t)
	p := h.open(t, "broken", audio.NewCaptureSink(mono16k))
	b, _ := h.v.Audio()
	if _, err := h.audioOut.Write([]byte{1, 2, 3}); err != nil {
		t.Fatal(err)
	}
	boom := errors.New("synthetic audio descriptor broke")
	if err := h.audioOut.CloseWithError(boom); err != nil {
		t.Fatal(err)
	}
	auditAudioJoin(t, "failed descriptor reader", h.v.pair.ended)
	auditAudioJoin(t, "failed output pump", p.Done())
	_, _, outErr := p.Errors()
	if !errors.Is(outErr, boom) {
		t.Fatalf("positive control: pump did not retain read error: %v", outErr)
	}
	select {
	case <-b.Released():
		t.Fatal("fault alone must not release engine custody")
	default:
	}
	select {
	case <-h.v.Untrusted():
		if !h.v.Faulted() {
			t.Error("Untrusted fired without fault state")
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatalf("pump retained %q and ended, but VoiceSession has no fault signal: Faulted=%v Alive=%v IsOpen=%v Label=%q", outErr, h.v.Faulted(), h.v.Alive(), h.v.IsOpen(), h.v.Label())
	}
}

// .
// .
func TestAuditClosingBlockedAudioDescriptorJoinsReaderAndPump(t *testing.T) {
	h := auditNewAudioHarness(t)
	p := h.open(t, "close", audio.NewCaptureSink(mono16k))
	if err := h.v.pair.out.Close(); err != nil {
		t.Fatal(err)
	}
	auditAudioJoin(t, "closed descriptor reader", h.v.pair.ended)
	auditAudioJoin(t, "closed descriptor pump", p.Done())
	_, _, outErr := p.Errors()
	if outErr != nil {
		t.Fatalf("owned descriptor closure unexpectedly retained a transport error: %v", outErr)
	}
}
