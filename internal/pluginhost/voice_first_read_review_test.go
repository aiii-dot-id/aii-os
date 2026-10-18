package pluginhost

import (
	"bytes"
	"context"
	"io"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/audio"
)

// Same supported scheduling boundary as the landed probe, moved from the
// payload read to the first positive read (the frame header).
//
// THIS STOOD SKIPPED, because it could not be closed from the host. The
// gate consumes a byte of the predecessor's frame and then blocks INSIDE
// the underlying Read; when Read returns, the predecessor has ended and
// the successor has attached, and every fact the host had was identical
// to the case where the successor's own first frame arrived while the
// reader waited. Any rule about WHO WAS ATTACHED that passed this test
// failed TestReviewCancelledBacklogCannotLeakIntoRecovery, and the
// reverse; both were implemented and measured.
//
// It closes because the transport now says whose frame it is (decided
// with the voice platform agent, who owns the wire): the engine names
// each output stream on the control lane, never reuses an id within a
// process, and the pair routes by that binding alone. The predecessor's
// frame is stream 41's, 41 was the predecessor's and retired with it,
// and no schedule around a blocking read can change that.
//
// ADAPTATION: the streams are bound the way the control lane binds them
// — 41 by the predecessor, 42 by the successor — through the adapter the
// loss probes use. The schedule and the assertion are as posted.
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

// No owner is an answer too: a frame that BEGAN arriving with no session
// attached must not be acquired by a session that attaches while its
// later bytes are still being read.
//
// ADAPTATION: this asked the question of owningReader, which sampled the
// attached session at a frame's first byte and is gone — ownership is no
// longer sampled at all. The property is kept and asked of the pair: the
// frame's stream is one the late session never names, so it is never
// that session's, and it is counted away when that session ends.
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
	// Its first byte is consumed and nobody is attached. Now somebody is.
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
