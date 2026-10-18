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

// NOTHING IS DROPPED WHILE SOMEBODY IS LISTENING. The reader used to
// discard a frame whenever its queue was full — a condition that, during
// an ACTIVE session, means only that the consumer is a few hundred
// frames behind. So words went missing mid-utterance while playback
// carried on, and nothing said so: the counter was named for the benign
// tail case, nothing in the tree read it, and unlike a shed critical
// event it did not fault the session.
//
// This sends far more frames than the queue holds, through a consumer
// that is deliberately slower than the writer, and asks for every one of
// them back in order.
func TestNoFrameIsLostWhileASessionIsAttached(t *testing.T) {
	pr, pw := io.Pipe()
	p := newAudioPair(io.Discard, pr)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	t0 := p.attach(ctx)
	ch := p.channel(t0, ctx)
	if err := p.bind(1, t0, "reply"); err != nil { // the control lane's word: stream 1 is this session's
		t.Fatal(err)
	}

	const frames = pairBuffer * 4 // four times what it can hold
	go func() {
		defer pw.Close()
		for i := 0; i < frames; i++ {
			fr := audio.Frame{Kind: audio.KindPCM, Stream: 1, Seq: uint32(i), PCM: []byte{byte(i), byte(i >> 8)}}
			if err := audio.WriteFrame(pw, fr); err != nil {
				return
			}
		}
	}()

	// A consumer that cannot keep up. Without backpressure the writer
	// runs ahead, the queue fills, and the frames it could not hold are
	// gone.
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

// AND THE TAIL OF AN ENDED SESSION IS STILL DROPPED, which is the one
// case that drop was ever for: those frames belong to a session that is
// over and must never be handed to the next one.
func TestOutputWithNoSessionToTakeItIsStillCountedAway(t *testing.T) {
	pr, pw := io.Pipe()
	p := newAudioPair(io.Discard, pr)
	defer pw.Close()

	// No session has ever attached: every frame is a tail frame.
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
	// And nothing of it is kept waiting for a name either: with no
	// session alive there is nobody who could give it one.
	if waiting := p.unnamedHeld(); waiting != 0 {
		t.Fatalf("%d frame(s) wait for a name nobody alive can give", waiting)
	}
}

// A SESSION ENDING RELEASES A WAITING READER. The reader blocks rather
// than discarding, so the thing that must never block is shutdown: a
// session that ends while a frame is in hand releases it, that frame is
// the tail, and the pump goes back to reading.
func TestASessionEndingReleasesAReaderWaitingOnAFullQueue(t *testing.T) {
	pr, pw := io.Pipe()
	p := newAudioPair(io.Discard, pr)
	ctx, cancel := context.WithCancel(context.Background())
	held := p.attach(ctx)
	if err := p.bind(1, held, "reply"); err != nil {
		t.Fatal(err)
	}

	// Nobody reads: fill the queue and leave the reader waiting on one
	// more frame it cannot place.
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

	// The session ends. The waiting reader must come back, count its
	// frame as tail, and keep reading — not sit on the descriptor.
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

// A BROKEN TRANSPORT IS NOT THE ENGINE FINISHING. Every ending used to
// arrive as io.EOF, so a truncated or corrupt stream reached the
// consumer as "it finished speaking" — the one reading it had no way to
// tell a completed reply from a cut one. The pump on the other side
// already keeps a non-EOF error as a fault and was simply never handed
// one (voice platform agent).
func TestATruncatedStreamIsReportedAsItselfNotAsACleanEnd(t *testing.T) {
	pr, pw := io.Pipe()
	p := newAudioPair(io.Discard, pr)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch := newSessionChannel(p, ctx)

	// Half a frame header, then the writer fails: the codec cannot make
	// a frame of it, and that is not an ending.
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

// AND A CLEAN END IS STILL A CLEAN END: the child closing its side is
// io.EOF, which is what a consumer treats as the reply being over.
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

// A SESSION THAT ATTACHES WHILE THE READER IS WAITING KEEPS ITS FIRST
// FRAME. The reader is already blocked in its read when the session
// arrives — it blocks until the engine speaks — so any rule that decides
// ownership from who was attached when the read BEGAN loses the very
// frame the session was waiting for. That is not hypothetical: an early
// fix did exactly that and broke every handoff proof in this package.
// Ownership is the stream's binding, so when the read began has nothing
// to do with it.
func TestASessionAttachingDuringABlockedReadKeepsItsFirstFrame(t *testing.T) {
	pr, pw := io.Pipe()
	p := newAudioPair(io.Discard, pr)
	defer pw.Close()

	// Let the reader reach its blocking read with nobody attached.
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

// A DEAD SESSION'S FAILURE IS NOT ITS SUCCESSOR'S. The transport fault
// now reaches the session, which means it must reach the RIGHT one: a
// pump that breaks after its session has been replaced would otherwise
// fault a healthy new conversation. The generation check is what stops
// it, and this is that check at the seam the fix relies on.
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

	// The old pump's reason arrives late, after its session is gone.
	h.v.apply(oldInst, func() { h.v.markFaultLocked("audio transport: the old descriptor broke") })
	if h.v.Faulted() {
		t.Fatal("a dead session's transport failure faulted the conversation that replaced it")
	}

	// And the live one can still be faulted, so the guard is a guard and
	// not a gag.
	h.v.apply(newInst, func() { h.v.markFaultLocked("audio transport: this descriptor broke") })
	if !h.v.Faulted() {
		t.Fatal("the live session's own transport failure was swallowed")
	}
}
