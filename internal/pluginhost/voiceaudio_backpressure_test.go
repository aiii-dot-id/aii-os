package pluginhost

import (
	"context"
	"errors"
	"io"
	"strings"
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
func TestNoFrameIsLostWhileASessionIsAttached(t *testing.T) {
	pr, pw := io.Pipe()
	p := newAudioPair(io.Discard, pr)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	t0 := p.attach(ctx)
	ch := p.channel(t0, ctx)
	if err := p.bind(1, t0, "reply"); err != nil {
		t.Fatal(err)
	}

	const frames = pairBuffer * 4
	go func() {
		defer pw.Close()
		for i := 0; i < frames; i++ {
			fr := audio.Frame{Kind: audio.KindPCM, Stream: 1, Seq: uint32(i), PCM: []byte{byte(i), byte(i >> 8)}}
			if err := audio.WriteFrame(pw, fr); err != nil {
				return
			}
		}
	}()

	// .
	// .
	// .
	for i := 0; i < frames; i++ {
		if i%64 == 0 {
			time.Sleep(time.Millisecond)
		}
		got, err := ch.ReadOutput()
		if err != nil {
			t.Fatalf("frame %d of %d never arrived: %v", i, frames, err)
		}
		if got.Seq != uint32(i) {
			t.Fatalf("frame %d arrived as %d — the stream skipped", i, got.Seq)
		}
	}
	if n := p.stray.Load(); n != 0 {
		t.Fatalf("%d frame(s) were counted away while a session was listening", n)
	}
}

// .
// .
// .
func TestOutputWithNoSessionToTakeItIsStillCountedAway(t *testing.T) {
	pr, pw := io.Pipe()
	p := newAudioPair(io.Discard, pr)
	defer pw.Close()

	// .
	for i := 0; i < 8; i++ {
		if err := audio.WriteFrame(pw, audio.Frame{Kind: audio.KindPCM, Stream: 1, Seq: uint32(i), PCM: []byte{1, 2}}); err != nil {
			t.Fatal(err)
		}
	}
	deadline := time.Now().Add(5 * time.Second)
	for p.stray.Load() < 8 {
		if time.Now().After(deadline) {
			t.Fatalf("frames with nobody to take them must be counted away: %d of 8", p.stray.Load())
		}
		time.Sleep(time.Millisecond)
	}
	if n := auditQueued(p); n != 0 {
		t.Fatalf("a frame with no session was queued for the next one: %d", n)
	}
	// .
	// .
	if waiting := p.unnamedHeld(); waiting != 0 {
		t.Fatalf("%d frame(s) wait for a name nobody alive can give", waiting)
	}
}

// .
// .
// .
// .
func TestASessionEndingReleasesAReaderWaitingOnAFullQueue(t *testing.T) {
	pr, pw := io.Pipe()
	p := newAudioPair(io.Discard, pr)
	ctx, cancel := context.WithCancel(context.Background())
	held := p.attach(ctx)
	if err := p.bind(1, held, "reply"); err != nil {
		t.Fatal(err)
	}

	// .
	// .
	go func() {
		for i := 0; i < pairBuffer+8; i++ {
			if err := audio.WriteFrame(pw, audio.Frame{Kind: audio.KindPCM, Stream: 1, Seq: uint32(i), PCM: []byte{1, 2}}); err != nil {
				return
			}
		}
	}()
	deadline := time.Now().Add(5 * time.Second)
	for p.queuedFor(held) < pairBuffer {
		if time.Now().After(deadline) {
			t.Fatalf("the queue never filled: %d", p.queuedFor(held))
		}
		time.Sleep(time.Millisecond)
	}

	// .
	// .
	cancel()
	for time.Now().Before(deadline) {
		if p.stray.Load() > 0 {
			pw.Close()
			return
		}
		time.Sleep(time.Millisecond)
	}
	pw.Close()
	t.Fatal("a session ending did not release the reader waiting to hand over a frame")
}

// .
// .
// .
// .
// .
// .
func TestATruncatedStreamIsReportedAsItselfNotAsACleanEnd(t *testing.T) {
	pr, pw := io.Pipe()
	p := newAudioPair(io.Discard, pr)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch := newSessionChannel(p, ctx)

	// .
	// .
	go func() {
		_, _ = pw.Write([]byte{0x01, 0x02, 0x03})
		_ = pw.CloseWithError(errors.New("the engine's audio descriptor broke"))
	}()

	_, err := ch.ReadOutput()
	if err == nil {
		t.Fatal("a broken transport must not read as a frame")
	}
	if errors.Is(err, io.EOF) {
		t.Fatalf("a broken transport was reported as a clean end: %v", err)
	}
	if !strings.Contains(err.Error(), "descriptor broke") && !strings.Contains(err.Error(), "unexpected EOF") {
		t.Fatalf("the reason did not survive to the consumer: %v", err)
	}
}

// .
// .
func TestAChildClosingItsSideIsAnOrdinaryEnd(t *testing.T) {
	pr, pw := io.Pipe()
	p := newAudioPair(io.Discard, pr)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch := newSessionChannel(p, ctx)
	if err := p.bind(1, ch.taker, "reply"); err != nil {
		t.Fatal(err)
	}

	if err := audio.WriteFrame(pw, audio.Frame{Kind: audio.KindPCM, Stream: 1, Seq: 1, PCM: []byte{7, 7}}); err != nil {
		t.Fatal(err)
	}
	if fr, err := ch.ReadOutput(); err != nil || fr.Seq != 1 {
		t.Fatalf("the frame before the end must arrive: %+v %v", fr, err)
	}
	pw.Close()
	if _, err := ch.ReadOutput(); !errors.Is(err, io.EOF) {
		t.Fatalf("a child closing its side is an ordinary end: %v", err)
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
func TestASessionAttachingDuringABlockedReadKeepsItsFirstFrame(t *testing.T) {
	pr, pw := io.Pipe()
	p := newAudioPair(io.Discard, pr)
	defer pw.Close()

	// .
	time.Sleep(20 * time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch := newSessionChannel(p, ctx)
	if err := p.bind(7, ch.taker, "reply"); err != nil {
		t.Fatal(err)
	}

	go func() {
		_ = audio.WriteFrame(pw, audio.Frame{Kind: audio.KindPCM, Stream: 7, Seq: 1, PCM: []byte{9, 9}})
	}()

	got, err := ch.ReadOutput()
	if err != nil {
		t.Fatalf("the session that attached lost its own first frame: %v", err)
	}
	if got.Stream != 7 || got.Seq != 1 {
		t.Fatalf("the wrong frame arrived: %+v", got)
	}
	if n := p.stray.Load(); n != 0 {
		t.Fatalf("its own first frame was counted away as a tail: %d", n)
	}
}

// .
// .
// .
// .
// .
func TestAStaleAudioFailureCannotFaultItsReplacement(t *testing.T) {
	h := auditNewAudioHarness(t)
	oldPump := h.open(t, "old", audio.NewCaptureSink(mono16k))
	h.v.mu.Lock()
	oldInst := h.v.inst
	h.v.mu.Unlock()
	h.terminal(t, "old")
	auditAudioJoin(t, "old pump", oldPump.Done())

	h.open(t, "new", audio.NewCaptureSink(mono16k))
	h.v.mu.Lock()
	newInst := h.v.inst
	h.v.mu.Unlock()
	if oldInst == newInst {
		t.Fatal("precondition: the new session did not take a new instance")
	}

	// .
	h.v.apply(oldInst, func() { h.v.markFaultLocked("audio transport: the old descriptor broke") })
	if h.v.Faulted() {
		t.Fatal("a dead session's transport failure faulted the conversation that replaced it")
	}

	// .
	// .
	h.v.apply(newInst, func() { h.v.markFaultLocked("audio transport: this descriptor broke") })
	if !h.v.Faulted() {
		t.Fatal("the live session's own transport failure was swallowed")
	}
}
