package pluginhost

import (
	"bytes"
	"context"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/audio"
)

// Pause after the predecessor's complete PCM has been consumed from the pipe,
// before ReadFrame returns to the owner-selection code. This is a scheduling
// boundary, not corrupt audio and not a write after the predecessor retired.
type reviewReadHandoffGate struct {
	io.ReadCloser
	remaining int
	read      chan struct{}
	release   chan struct{}
	once      sync.Once
}

func (g *reviewReadHandoffGate) Read(b []byte) (int, error) {
	n, err := g.ReadCloser.Read(b)
	g.remaining -= n
	if g.remaining <= 0 {
		g.once.Do(func() { close(g.read); <-g.release })
	}
	return n, err
}

// ADAPTATION (audio ownership): streams are bound the way the control
// lane binds them — 41 by the predecessor, 42 by the successor — through
// the same adapter the loss probes use. The schedule and the assertion
// are as posted.
func TestReviewDecodedPredecessorFrameCannotJoinSuccessor(t *testing.T) {
	var packet bytes.Buffer
	old := audio.Frame{Kind: audio.KindPCM, Stream: 41, Seq: 1, Start: 0, PCM: []byte{1, 2}}
	if err := audio.WriteFrame(&packet, old); err != nil {
		t.Fatal(err)
	}
	r, w := io.Pipe()
	gate := &reviewReadHandoffGate{ReadCloser: r, remaining: packet.Len(), read: make(chan struct{}), release: make(chan struct{})}
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
		t.Fatal("predecessor frame was not read")
	}
	cancelA()
	ctxB, cancelB := context.WithCancel(context.Background())
	defer cancelB()
	b := reviewAttachAudio(pair, ctxB, 42)
	close(gate.release)
	go func() {
		_ = audio.WriteFrame(w, audio.Frame{Kind: audio.KindPCM, Stream: 42, Seq: 1, PCM: []byte{3, 4}})
	}()
	result := make(chan audio.Frame, 1)
	go func() { f, _ := b.ReadOutput(); result <- f }()
	select {
	case frame := <-result:
		if frame.Stream != 42 {
			t.Fatalf("predecessor's in-flight decoded frame assigned to successor: stream=%d PCM=%v; want 42", frame.Stream, frame.PCM)
		}
	case <-time.After(time.Second):
		t.Fatal("successor stalled")
	}
}
