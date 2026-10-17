package pluginhost

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/audio"
)

// WHOSE AUDIO IS IT — the acceptance the voice platform agent set for
// closing cross-session ownership: delayed old-session bytes, audio
// observed before its mapping, a successor attaching during a blocked
// read, a process restart, and interruption with recovery. Each runs
// through the consumer — the real session driver with its control lane,
// its pump and a speaker — because the table is only as good as the
// production path that fills it.

func ownFrame(t *testing.T, h *auditAudioHarness, seq, stream uint32) {
	t.Helper()
	if err := h.frame(seq, stream); err != nil {
		t.Fatal(err)
	}
}

func ownWaiting(p *audioPair) int { return p.unnamedHeld() }

func ownHeard(s *audio.CaptureSink, stream uint32) (seqs []uint32) {
	for _, fr := range s.Frames() {
		if fr.Stream == stream {
			seqs = append(seqs, fr.Seq)
		}
	}
	return seqs
}

// 1. DELAYED OLD-SESSION BYTES. A reply's tail written before its
// session ended, and delivered by the descriptor only after a successor
// is attached and speaking, is the old session's still. So is a stream
// the old session opened whose control word was overtaken by the
// successor's open: its audio arrives unnamed, waits, and is disowned
// when the late word says whose it was.
func TestDelayedOldSessionBytesNeverReachTheSuccessor(t *testing.T) {
	h := auditNewAudioHarness(t)
	oldSink := audio.NewCaptureSink(mono16k)
	oldPump := h.open(t, "old", oldSink)
	h.name("old", 11)
	ownFrame(t, h, 1, 11)
	auditAudioWait(t, "the old session hears its own reply", func() bool { return len(ownHeard(oldSink, 11)) == 1 })
	h.terminal(t, "old")
	auditAudioJoin(t, "old pump", oldPump.Done())

	fresh := audio.NewCaptureSink(mono16k)
	h.open(t, "new", fresh)
	h.name("new", 22)
	for i := uint32(2); i <= 9; i++ {
		ownFrame(t, h, i, 11) // the named reply's tail, late
	}
	for i := uint32(1); i <= 4; i++ {
		ownFrame(t, h, i, 12) // a reply the successor has not heard named, and never will name
	}
	h.frames <- []byte(`{"jsonrpc":"2.0","method":"session.event","params":{"type":"synthesis_start","session_id":"old","synthesis_id":"late","output_stream":12}}`)
	ownFrame(t, h, 9001, 22)

	auditAudioWait(t, "the successor hears its own reply", func() bool { return len(ownHeard(fresh, 22)) == 1 })
	auditAudioWait(t, "every old byte is counted away", func() bool { return h.v.Stray() == 12 })
	for _, fr := range fresh.Frames() {
		if fr.Stream != 22 {
			t.Fatalf("the successor's speaker played stream %d (seq %d) — a session that was over", fr.Stream, fr.Seq)
		}
	}
	if n := len(oldSink.Frames()); n != 1 {
		t.Fatalf("the ended session's speaker was written to after its end: %d frames", n)
	}
	if h.v.Faulted() {
		t.Fatalf("an ended session's late word is not its successor's fault: %s", h.v.FaultReason())
	}
	if n := ownWaiting(h.v.pair); n != 0 {
		t.Fatalf("%d old frame(s) still wait for a name", n)
	}
}

// 2. AUDIO OBSERVED BEFORE ITS MAPPING. The lanes are separate
// descriptors: the engine sending synthesis_start first does not mean
// the host processes it first. Audio for a stream nobody has named is
// neither played nor lost — it waits, and when the word comes it is
// delivered whole and in order, with what follows it.
func TestAudioObservedBeforeItsMappingWaitsForItsNameAndArrivesWholeAndInOrder(t *testing.T) {
	h := auditNewAudioHarness(t)
	sink := audio.NewCaptureSink(mono16k)
	h.open(t, "s", sink)
	const early, late = 40, 40
	for i := uint32(0); i < early; i++ {
		ownFrame(t, h, i, 5)
	}
	auditAudioWait(t, "the unnamed audio is held", func() bool { return ownWaiting(h.v.pair) == early })
	if n := len(sink.Frames()); n != 0 {
		t.Fatalf("%d frame(s) of a stream nobody had named reached the speaker: the host guessed", n)
	}
	if h.v.Stray() != 0 || h.v.Unnamed() != 0 {
		t.Fatalf("audio waiting for its name was discarded: stray=%d unnamed=%d", h.v.Stray(), h.v.Unnamed())
	}

	h.name("s", 5)
	for i := uint32(early); i < early+late; i++ {
		ownFrame(t, h, i, 5)
	}
	auditAudioWait(t, "the whole reply reaches the speaker", func() bool { return len(ownHeard(sink, 5)) == early+late })
	for i, seq := range ownHeard(sink, 5) {
		if seq != uint32(i) {
			t.Fatalf("frame %d arrived as %d: what waited for the name and what followed it changed places", i, seq)
		}
	}
	if h.v.Stray() != 0 || h.v.Unnamed() != 0 || h.v.Faulted() {
		t.Fatalf("stray=%d unnamed=%d faulted=%v (%s)", h.v.Stray(), h.v.Unnamed(), h.v.Faulted(), h.v.FaultReason())
	}
}

// A whole short reply — END included — can arrive before its name. It
// is delivered whole, and its stream is over: nothing may bind it again.
func TestAWholeReplyThatArrivesBeforeItsNameIsDeliveredAndItsStreamIsOver(t *testing.T) {
	h := auditNewAudioHarness(t)
	sink := audio.NewCaptureSink(mono16k)
	h.open(t, "s", sink)
	ownFrame(t, h, 0, 6)
	ownFrame(t, h, 1, 6)
	if err := audio.WriteFrame(h.audioOut, audio.Frame{Kind: audio.KindEnd, Stream: 6, Seq: 2, Start: 2}); err != nil {
		t.Fatal(err)
	}
	auditAudioWait(t, "the reply waits", func() bool { return ownWaiting(h.v.pair) == 3 })
	h.name("s", 6)
	auditAudioWait(t, "the reply and its END reach the speaker", func() bool {
		_, p := h.v.Audio()
		st, ok := p.OutputStream(6)
		return ok && st.Ended && len(ownHeard(sink, 6)) == 3
	})
	h.v.pair.omu.Lock()
	_, live := h.v.pair.bound[6]
	over := h.v.pair.retiredLocked(6)
	h.v.pair.omu.Unlock()
	if live || !over {
		t.Fatalf("a stream whose END has passed is still open on the wire: bound=%v retired=%v", live, over)
	}
}

// 3. A SUCCESSOR ATTACHING DURING A BLOCKED READ. The predecessor's
// frame is inside the reader — its first byte consumed, the read blocked
// — while the predecessor ends and the successor opens, names its own
// stream and starts listening. This is the schedule no rule about who
// was attached could answer; here it runs through the real driver.
func TestASuccessorOpeningInsideABlockedFirstReadNeverHearsThePredecessor(t *testing.T) {
	h := auditNewAudioHarness(t)
	unused := h.v.pair
	_ = unused.out.Close()
	_ = h.audioOut.Close()
	auditAudioJoin(t, "initial unused pair", unused.ended)
	rd, wr, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	gate := &reviewReadHandoffGate{ReadCloser: rd, remaining: 1, read: make(chan struct{}), release: make(chan struct{})}
	h.v.pair = newAudioPair(io.Discard, gate)
	released := false
	t.Cleanup(func() {
		if !released {
			close(gate.release)
		}
		_ = rd.Close()
		_ = wr.Close()
	})

	oldSink := audio.NewCaptureSink(mono16k)
	oldPump := h.open(t, "old", oldSink)
	h.name("old", 71)
	// The whole frame is in the OS pipe before anything else happens.
	if err := audio.WriteFrame(wr, audio.Frame{Kind: audio.KindPCM, Stream: 71, Seq: 7, PCM: bytes.Repeat([]byte{0x34, 0x12}, 240)}); err != nil {
		t.Fatal(err)
	}
	auditAudioJoin(t, "the frame's first byte is consumed and the read is held", gate.read)
	h.terminal(t, "old")
	auditAudioJoin(t, "old pump", oldPump.Done())

	fresh := audio.NewCaptureSink(mono16k)
	h.open(t, "new", fresh)
	h.name("new", 72)
	released = true
	close(gate.release)
	if err := audio.WriteFrame(wr, audio.Frame{Kind: audio.KindPCM, Stream: 72, Seq: 99, PCM: []byte{1, 2}}); err != nil {
		t.Fatal(err)
	}
	auditAudioWait(t, "the successor hears its own first frame", func() bool { return len(ownHeard(fresh, 72)) == 1 })
	if got := ownHeard(fresh, 71); len(got) != 0 {
		t.Fatalf("the successor played the frame its predecessor was cut off in: %v", got)
	}
	auditAudioWait(t, "the predecessor's frame is counted away", func() bool { return h.v.Stray() == 1 })
	if n := len(oldSink.Frames()); n != 0 {
		t.Fatalf("an ended session's speaker was written to: %d", n)
	}
}

// 4. A PROCESS RESTART IS A SEPARATE NAMESPACE. An engine's counter
// starts again with its process, so the restarted child's first reply is
// stream 1 exactly as its predecessor's was. The table belongs to the
// spawn's pair, so that is a new stream 1 and it is delivered — where a
// table that outlived the process would refuse it as retired — and the
// dead process's table stays closed.
func TestARestartedProcessHasItsOwnStreamNamespace(t *testing.T) {
	ap := audioVoicePlugin(t, []string{fakechildBin, "session-audio"})
	ctx := context.Background()

	speak := func(v *VoiceSession, id string) (*audio.CaptureSink, *audio.Binding) {
		t.Helper()
		plane := audio.NewPlane()
		src, err := audio.NewFileSource(bytes.NewReader(pcmRamp(1000)), mono16k, 320)
		if err != nil {
			t.Fatal(err)
		}
		sink := audio.NewCaptureSink(mono16k)
		_ = plane.Register(&audio.Endpoint{ID: "mic", Label: "file", Source: src})
		_ = plane.Register(&audio.Endpoint{ID: "spk", Label: "capture", Sink: sink})
		b, err := plane.Bind(id, "mic", "spk", true)
		if err != nil {
			t.Fatal(err)
		}
		if err := v.OpenWithAudio(ctx, id, b, nil); err != nil {
			t.Fatalf("open %s: %v", id, err)
		}
		deadline := time.Now().Add(10 * time.Second)
		for sink.End() != 1000 && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
		}
		if sink.End() != 1000 || !bytes.Equal(sink.PCM(), pcmRamp(1000)) {
			t.Fatalf("session %s: the reply did not arrive whole: end=%d faulted=%v (%s) stray=%d", id, sink.End(), v.Faulted(), v.FaultReason(), v.Stray())
		}
		for _, fr := range sink.Frames() {
			if fr.Stream != 1 {
				t.Fatalf("session %s: the fixture's first reply is stream 1 in each process: %+v", id, fr)
			}
		}
		return sink, b
	}

	first := ap.Voice.Load()
	speak(first, "before")
	firstPair := first.pair
	firstPair.omu.Lock()
	over := firstPair.retiredLocked(1)
	firstPair.omu.Unlock()
	if !over {
		t.Fatal("precondition: the first process's stream 1 has ended and is retired in its table")
	}

	// The child dies where it stands, with the session open.
	if killed, err := ap.sup.KillChild(); err != nil || !killed {
		t.Fatalf("no live child was killed: %v", err)
	}
	if !fired(first.Done(), 10*time.Second) {
		t.Fatal("the application must learn the session died")
	}
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if v := ap.Voice.Load(); ap.sup.Restarts() > 0 && v != first && v.Alive() {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	second := ap.Voice.Load()
	if second == first || second.pair == nil || second.pair == firstPair {
		t.Fatalf("the restarted child must have a driver and a pair of its own: same driver=%v", second == first)
	}
	t.Cleanup(ap.sessionCancel)

	speak(second, "after")
	if second.Faulted() || second.Stray() != 0 || second.Unnamed() != 0 {
		t.Fatalf("the restarted process's own stream 1 was treated as its predecessor's: faulted=%v (%s) stray=%d unnamed=%d",
			second.Faulted(), second.FaultReason(), second.Stray(), second.Unnamed())
	}

	// And the dead process's namespace stays closed.
	auditAudioJoin(t, "the dead process's reader", firstPair.ended)
	live, cancel := context.WithCancel(ctx)
	defer cancel()
	if err := firstPair.bind(1, firstPair.attach(live), "again"); err == nil {
		t.Fatal("a retired stream was bound again in the table it was retired in")
	}
}

// 5. INTERRUPTION AND RECOVERY. The operator speaks over a reply. The
// page reports the playback stopped — the last word on that stream — and
// the engine starts a recovery reply on a new one. The rest of the
// interrupted reply is still on the descriptor: it must not be played
// into the recovery, the recovery must arrive whole even where its audio
// beats its name, and the interrupted stream can never be opened again.
func TestAnInterruptedReplysTailNeverPlaysIntoTheRecovery(t *testing.T) {
	h := auditNewAudioHarness(t)
	sink := audio.NewCaptureSink(mono16k)
	h.open(t, "talk", sink)
	h.name("talk", 31)
	for i := uint32(0); i < 5; i++ {
		ownFrame(t, h, i, 31)
	}
	auditAudioWait(t, "the reply is playing", func() bool { return len(ownHeard(sink, 31)) == 5 })

	if err := h.v.PlaybackReportFor(h.ctx, "talk", PlaybackReport{Stream: 31, Rendered: 0, Rate: mono16k.Rate, Channels: mono16k.Channels, Terminal: true, Outcome: "stopped"}); err != nil {
		t.Fatalf("the page's stop report: %v", err)
	}

	// What the engine had already written of the interrupted reply...
	for i := uint32(5); i < 25; i++ {
		ownFrame(t, h, i, 31)
	}
	// ...and the recovery, its first audio ahead of its name.
	for i := uint32(0); i < 3; i++ {
		ownFrame(t, h, i, 32)
	}
	h.name("talk", 32)
	for i := uint32(3); i < 10; i++ {
		ownFrame(t, h, i, 32)
	}

	auditAudioWait(t, "the recovery arrives whole", func() bool { return len(ownHeard(sink, 32)) == 10 })
	for i, seq := range ownHeard(sink, 32) {
		if seq != uint32(i) {
			t.Fatalf("recovery frame %d arrived as %d", i, seq)
		}
	}
	if got := ownHeard(sink, 31); len(got) != 5 {
		t.Fatalf("the interrupted reply went on playing after its stop was reported: %d frames, want the 5 before it", len(got))
	}
	auditAudioWait(t, "the interrupted tail is counted away", func() bool { return h.v.Stray() == 20 })
	if h.v.Faulted() {
		t.Fatalf("an interruption is not a fault: %s", h.v.FaultReason())
	}

	// The interrupted stream is over for good: a word that opens it
	// again is the engine reusing an id, and the session is told.
	h.name("talk", 31)
	auditAudioWait(t, "the session learns its engine reused a stream", h.v.Faulted)
	if why := h.v.FaultReason(); !strings.Contains(why, "output stream 31") || !strings.Contains(why, "retired") {
		t.Fatalf("the fault does not say what happened: %q", why)
	}
}

// RETIRED MEANS NEVER AGAIN, ACROSS SESSIONS TOO. A successor whose
// engine names a stream its predecessor used is not handed that stream:
// the binding is refused, the successor is told, and what arrives on the
// stream is still counted away.
func TestASuccessorCannotBeGivenAStreamItsPredecessorUsed(t *testing.T) {
	h := auditNewAudioHarness(t)
	oldPump := h.open(t, "old", audio.NewCaptureSink(mono16k))
	h.name("old", 11)
	ownFrame(t, h, 1, 11)
	h.terminal(t, "old")
	auditAudioJoin(t, "old pump", oldPump.Done())

	fresh := audio.NewCaptureSink(mono16k)
	h.open(t, "new", fresh)
	h.name("new", 11)
	auditAudioWait(t, "the successor is told", h.v.Faulted)
	if why := h.v.FaultReason(); !strings.Contains(why, "output stream 11") {
		t.Fatalf("the fault does not name the stream: %q", why)
	}
	ownFrame(t, h, 2, 11)
	auditAudioWait(t, "the frame is counted away", func() bool { return h.v.Stray() >= 1 })
	if n := len(fresh.Frames()); n != 0 {
		t.Fatalf("the successor's speaker played %d frame(s) of a reused stream", n)
	}
}

// THE WAIT FOR A NAME IS BOUNDED, AND WHAT IT COSTS IS SAID. An engine
// that speaks on a stream it never introduces fills the storage; the
// reader waits a bounded while for the control lane, then gives that
// stream up — discarded, counted apart from tails, retired. Audio that
// HAS a name keeps flowing behind it, and a word that names the
// discarded stream afterwards is refused and its session told, rather
// than handed a reply with its beginning missing.
func TestAnUnnamedStreamIsGivenUpAfterABoundedWaitAndANameAfterwardsIsRefused(t *testing.T) {
	h := auditNewAudioHarness(t)
	h.v.pair.omu.Lock()
	h.v.pair.patience = 50 * time.Millisecond
	h.v.pair.omu.Unlock()
	sink := audio.NewCaptureSink(mono16k)
	h.open(t, "s", sink)
	h.name("s", 8)

	wrote := make(chan error, 1)
	go func() {
		for i := uint32(0); i < pendingFrames+10; i++ {
			if err := h.frame(i, 9); err != nil { // never named
				wrote <- err
				return
			}
		}
		wrote <- h.frame(1, 8) // named, behind it
	}()
	select {
	case err := <-wrote:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("an unnamed stream blocked the descriptor for good: the wait for a name is not bounded")
	}
	auditAudioWait(t, "named audio still flows", func() bool { return len(ownHeard(sink, 8)) == 1 })
	if got := h.v.Unnamed(); got != pendingFrames {
		t.Fatalf("unnamed=%d, want the %d that waited and were given up", got, pendingFrames)
	}
	auditAudioWait(t, "the rest of the abandoned stream is counted away", func() bool { return h.v.Stray() == 10 })
	if len(ownHeard(sink, 9)) != 0 {
		t.Fatal("audio nobody named reached the speaker")
	}
	if h.v.Faulted() {
		t.Fatalf("nobody has claimed the stream yet, so nobody is faulted: %s", h.v.FaultReason())
	}

	h.name("s", 9)
	auditAudioWait(t, "the session that names it is told", h.v.Faulted)
	if why := h.v.FaultReason(); !strings.Contains(why, "output stream 9") || !strings.Contains(why, "discarded") {
		t.Fatalf("the fault does not say the reply cannot be whole: %q", why)
	}
}

// A SESSION OPENED WITHOUT AUDIO CLAIMS NOTHING. The streams it names
// are retired as they are named, so an engine that speaks anyway fills
// no storage and reaches no later session.
func TestASessionWithoutAudioOwnsNoStreams(t *testing.T) {
	h := auditNewAudioHarness(t)
	if err := h.v.Open(h.ctx, "silent", nil); err != nil {
		t.Fatal(err)
	}
	h.name("silent", 3)
	auditAudioWait(t, "the word is heard", func() bool {
		h.v.pair.omu.Lock()
		defer h.v.pair.omu.Unlock()
		return h.v.pair.retiredLocked(3)
	})
	for i := uint32(0); i < 4; i++ {
		ownFrame(t, h, i, 3)
	}
	auditAudioWait(t, "its audio is counted away", func() bool { return h.v.Stray() == 4 })
	if n := ownWaiting(h.v.pair); n != 0 {
		t.Fatalf("%d frame(s) wait for a session that takes no audio", n)
	}
}

// THE TABLE ITSELF: the same word twice is one binding; one stream is
// one reply; and what it remembers is bounded however long the engine
// lives, without ever forgetting a stream it could still be asked about.
func TestTheOwnershipTableIsExactAndBounded(t *testing.T) {
	r, w := io.Pipe()
	defer r.Close()
	defer w.Close()
	p := newAudioPair(io.Discard, r)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	own := p.attach(ctx)

	if err := p.bind(1, own, "a"); err != nil {
		t.Fatal(err)
	}
	if err := p.bind(1, own, "a"); err != nil {
		t.Fatalf("the same word again is the same binding: %v", err)
	}
	if err := p.bind(1, own, "b"); err == nil {
		t.Fatal("one stream carried two replies")
	}
	other, cancelOther := context.WithCancel(context.Background())
	defer cancelOther()
	if err := p.bind(1, p.attach(other), "a"); err == nil {
		t.Fatal("one stream was bound to two sessions")
	}

	// Ids the engine allocated and never named leave holes: every other
	// id, far more of them than the table keeps exactly.
	for s := uint32(2); s < 2+4*retiredKept; s += 2 {
		p.retire(s)
	}
	p.omu.Lock()
	kept, floor := len(p.retired), p.floor
	p.omu.Unlock()
	if kept > retiredKept {
		t.Fatalf("the table remembers %d retired ids: it grows with the engine's life", kept)
	}
	if floor < 2 {
		t.Fatalf("the floor never rose: %d", floor)
	}
	for _, s := range []uint32{2, uint32(floor), 4*retiredKept - 2} {
		if err := p.bind(s, own, fmt.Sprint("again-", s)); err == nil {
			t.Fatalf("retired stream %d was bound again", s)
		}
	}
	// And the newest hole is still a hole, not a casualty of forgetting.
	if err := p.bind(4*retiredKept+1, own, "fresh"); err != nil {
		t.Fatalf("a stream that was never retired was refused: %v", err)
	}

	// The live table is bounded too: an engine that opens streams and
	// never ends one is told so, not tracked without limit.
	var refused error
	opened := 0
	for s := uint32(1 << 20); opened <= maxBoundStreams && refused == nil; s++ {
		if refused = p.bind(s, own, fmt.Sprint("open-", s)); refused == nil {
			opened++
		}
	}
	if refused == nil || !strings.Contains(refused.Error(), "output streams are open") {
		t.Fatalf("the live table accepted %d more streams without limit: %v", opened, refused)
	}
}

// BOUNDED IN BYTES TOO. Frames may be as large as the codec allows, so a
// count alone does not bound what waits for a name: the unnamed account
// stops at its byte budget, and what it gave up is what it held.
func TestTheWaitForANameIsBoundedInBytes(t *testing.T) {
	h := auditNewAudioHarness(t)
	h.v.pair.omu.Lock()
	h.v.pair.patience = 50 * time.Millisecond
	h.v.pair.omu.Unlock()
	h.open(t, "s", audio.NewCaptureSink(mono16k))
	big := make([]byte, audio.MaxFramePayload)
	fits := pendingBytes / audio.MaxFramePayload
	if fits >= pendingFrames {
		t.Skip("the frame budget binds first at this payload size")
	}
	wrote := make(chan error, 1)
	go func() {
		for i := 0; i < fits+3; i++ {
			if err := audio.WriteFrame(h.audioOut, audio.Frame{Kind: audio.KindPCM, Stream: 9, Seq: uint32(i), Start: int64(i), PCM: big}); err != nil {
				wrote <- err
				return
			}
		}
		wrote <- nil
	}()
	select {
	case err := <-wrote:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the writer never got past the byte budget: the wait is not bounded")
	}
	if got := h.v.Unnamed(); got != uint64(fits) {
		t.Fatalf("unnamed=%d, want the %d frames the byte budget held", got, fits)
	}
	auditAudioWait(t, "the rest is counted away", func() bool { return h.v.Stray() == 3 })
}

// A SLOW SPEAKER IS WAITED FOR, NOT MISTAKEN FOR A MISSING NAME. With the
// store full of the session's own audio, the next reply's first frame has
// nowhere to go — and the patience that gives up never-named audio must
// not run: nothing is dropped while somebody is listening. It waits in
// the reader's hand, takes its name while it waits, and arrives with
// everything before it once the speaker moves.
func TestASlowSpeakerIsWaitedForNotMistakenForAMissingName(t *testing.T) {
	h := auditNewAudioHarness(t)
	h.v.pair.omu.Lock()
	h.v.pair.patience = 20 * time.Millisecond
	h.v.pair.omu.Unlock()
	sink := auditNewHeldSink()
	h.open(t, "s", sink)
	h.name("s", 1)
	ownFrame(t, h, 0, 1)
	auditAudioJoin(t, "speaker blocked", sink.started)
	for _, stream := range []uint32{2, 3} {
		for i := uint32(0); i < pendingFrames; i++ {
			ownFrame(t, h, i, stream)
		}
		auditAudioWait(t, "an early reply waits", func() bool { return ownWaiting(h.v.pair) == pendingFrames })
		h.name("s", stream)
		auditAudioWait(t, "and is the session's once named", func() bool { return ownWaiting(h.v.pair) == 0 })
	}
	if got := auditQueued(h.v.pair); got != storeFrames {
		t.Fatalf("fixture: the store should be full of the session's own audio: %d of %d", got, storeFrames)
	}
	ownFrame(t, h, 0, 4) // read by the reader, and held in its hand: there is no room
	time.Sleep(15 * h.v.pair.patience)
	if h.v.Unnamed() != 0 || h.v.Stray() != 0 || h.v.Faulted() {
		t.Fatalf("a slow speaker cost the session audio: unnamed=%d stray=%d faulted=%v (%s)", h.v.Unnamed(), h.v.Stray(), h.v.Faulted(), h.v.FaultReason())
	}
	h.name("s", 4)
	close(sink.resume)
	auditAudioWait(t, "everything arrives once the speaker moves", func() bool {
		return len(ownHeard(sink.capture, 1)) == 1 && len(ownHeard(sink.capture, 2)) == pendingFrames &&
			len(ownHeard(sink.capture, 3)) == pendingFrames && len(ownHeard(sink.capture, 4)) == 1
	})
	for _, stream := range []uint32{2, 3} {
		for i, seq := range ownHeard(sink.capture, stream) {
			if seq != uint32(i) {
				t.Fatalf("stream %d frame %d arrived as %d", stream, i, seq)
			}
		}
	}
	if h.v.Unnamed() != 0 || h.v.Stray() != 0 {
		t.Fatalf("unnamed=%d stray=%d", h.v.Unnamed(), h.v.Stray())
	}
}
