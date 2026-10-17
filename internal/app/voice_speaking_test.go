package app

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost/pluginhosttest"
)

// WHETHER A SESSION IS SPEAKING IS THE HOST'S OWN KNOWLEDGE (voiceHandle.
// speaking). These tests never set the answer: each drives the handle
// through the application's own admission of a reply, the page's playback
// reports and the fences, on the stand-in engine session, and reads what
// the handle then says.
//
// The three helpers below are for any test that needs a session in that
// state (the hold of the memory backfill is one).

// speakerSession is a session that only speaks, on the stand-in engine,
// held by a as the application holds an open session.
func speakerSession(t testing.TB, a *App, id string) (*voiceHandle, *fakeEngineSession) {
	t.Helper()
	f := &fakeEngineSession{streams: map[uint32]string{}}
	h := &voiceHandle{id: id, v: f}
	a.voiceSessions.Store(id, h)
	return h, f
}

// admitSpeakerReply admits one reply for synthesis on h through the
// application's admission and names the output stream the engine gave it,
// as its synthesis_start would. It returns the synthesis.
func admitSpeakerReply(t testing.TB, a *App, h *voiceHandle, f *fakeEngineSession, stream uint32, text string) string {
	t.Helper()
	verdict, synth := a.admitReply(t.Context(), h.id, h.gen.Load(), text)
	if verdict != replyAdmitted || synth == "" {
		t.Fatalf("the reply %q was not admitted for synthesis: verdict %v, synthesis %q", text, verdict, synth)
	}
	f.mu.Lock()
	f.streams[stream] = synth
	f.mu.Unlock()
	return synth
}

// playbackOf files the page's playback report for one output stream
// through the handle: a progress report, or the stream's terminal one.
func playbackOf(t testing.TB, h *voiceHandle, stream uint32, terminal bool) {
	t.Helper()
	r := dashboard.PlaybackReport{SessionID: h.id, Stream: stream, Rendered: 4800, Rate: 48000, Channels: 1, Outcome: "progress"}
	if terminal {
		r.Terminal, r.Outcome = true, "drained"
	}
	if err := h.PlaybackReport(t.Context(), r); err != nil {
		t.Fatalf("the page's playback report for stream %d: %v", stream, err)
	}
}

// A session is speaking from the moment its reply is on the wire, before
// the engine has acknowledged it or said anything of it: the start of a
// reply is when a listener notices a delay most.
func TestASessionIsSpeakingFromTheEnqueueOfItsReplyBeforeTheEngineAnswers(t *testing.T) {
	a := &App{cfg: &Config{}}
	h, f := speakerSession(t, a, "v")
	if h.speaking() {
		t.Fatal("a session nothing was asked of says it is speaking")
	}
	f.entered, f.ackGate = make(chan struct{}), make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(f.ackGate) }) }
	defer release()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	admitted := make(chan replyVerdict, 1)
	go func() { admitted <- a.synthesizeReply(ctx, h.id, 0, "a reply") }()
	select {
	case <-f.entered:
	case <-ctx.Done():
		t.Fatal("the reply never reached the engine")
	}
	// The engine holds the text and has not acknowledged it. The record is
	// written under the same lock as the enqueue, a moment after it: wait
	// for that, bounded, and never for the acknowledgement.
	voiceWait(t, "the session to say it is speaking while its reply's acknowledgement is withheld", 2*time.Second, h.speaking)
	select {
	case v := <-admitted:
		t.Fatalf("the admission returned (%v) while its acknowledgement was withheld", v)
	default:
	}
	release()
	if v := <-admitted; v != replyAdmitted {
		t.Fatalf("the reply was not admitted: %v", v)
	}
	if !h.speaking() {
		t.Fatal("an admitted reply is not out")
	}
}

// Progress is not the end of a reply; its stream's terminal report is.
func TestAReplyIsOutThroughItsProgressAndOverAtItsTerminalReport(t *testing.T) {
	a := &App{cfg: &Config{}}
	h, f := speakerSession(t, a, "v")
	admitSpeakerReply(t, a, h, f, 3, "a reply")
	playbackOf(t, h, 3, false)
	if !h.speaking() {
		t.Fatal("a progress report ended the reply")
	}
	// A terminal report of a stream the engine never named settles nothing.
	playbackOf(t, h, 9, true)
	if !h.speaking() {
		t.Fatal("the end of another, unnamed stream ended this reply")
	}
	playbackOf(t, h, 3, true)
	if h.speaking() {
		t.Fatal("the reply's terminal playback report was accepted and the session still says it is speaking")
	}
}

// A fence the engine admits ends the reply. One it refuses does not: the
// engine may still be speaking, and only the reply's own end (or the
// bound) says otherwise.
func TestAnAdmittedFenceEndsAReplyAndARefusedFenceLeavesItOut(t *testing.T) {
	a := &App{cfg: &Config{}}
	h, f := speakerSession(t, a, "v")
	admitSpeakerReply(t, a, h, f, 1, "stopped by its listener")
	if err := h.Interrupt(t.Context()); err != nil {
		t.Fatal(err)
	}
	if h.speaking() {
		t.Fatal("the engine admitted the stop and the cancel, and the session still says it is speaking")
	}

	admitSpeakerReply(t, a, h, f, 2, "a stop the engine refuses")
	f.mu.Lock()
	f.failInterrupt = errors.New("fixture: stop refused")
	f.mu.Unlock()
	if err := h.Interrupt(t.Context()); err == nil {
		t.Fatal("fixture: the refused stop reported success")
	}
	if !h.speaking() {
		t.Fatal("a fence the engine refused ended the reply: it may still be speaking")
	}
	playbackOf(t, h, 2, true)
	if h.speaking() {
		t.Fatal("the reply's terminal report did not end it after a refused fence")
	}
}

// A fence of "the engine's current generation" (no reply named) ends the
// reply that was out when it was sent.
func TestAFenceOfTheCurrentGenerationEndsTheReplyThatWasOut(t *testing.T) {
	a := &App{cfg: &Config{}}
	h, f := speakerSession(t, a, "v")
	admitSpeakerReply(t, a, h, f, 1, "a reply")
	if err := h.fence(t.Context(), "", "fixture: stop whatever is speaking"); err != nil {
		t.Fatal(err)
	}
	if h.speaking() {
		t.Fatal("the engine admitted a stop of its current generation and the session still says it is speaking")
	}
}

// fencedMidway is the stand-in engine with a moment inside its fence: after
// the engine admitted the controls and before the host is told so.
type fencedMidway struct {
	*fakeEngineSession
	during func()
}

func (f *fencedMidway) InterruptFor(ctx context.Context, sid, synth, why string) error {
	err := f.fakeEngineSession.InterruptFor(ctx, sid, synth, why)
	if f.during != nil {
		f.during()
	}
	return err
}

// A fence that names no reply ends the one that was out WHEN IT WAS SENT.
// A reply admitted while the fence was on its way is a later reply: the
// stop was not for it.
func TestAFenceOfTheCurrentGenerationDoesNotEndAReplyAdmittedWhileItWasOnItsWay(t *testing.T) {
	a := &App{cfg: &Config{}}
	h, f := speakerSession(t, a, "v")
	midway := &fencedMidway{fakeEngineSession: f}
	h.v = midway
	admitSpeakerReply(t, a, h, f, 1, "the reply the stop is for")
	midway.during = func() { h.replyAdmitted("a-later-reply") }
	if err := h.fence(t.Context(), "", "fixture: stop whatever is speaking"); err != nil {
		t.Fatal(err)
	}
	if out := h.out.Load(); out == nil || out.id != "a-later-reply" || !h.speaking() {
		t.Fatalf("a stop sent before a reply was admitted ended that reply: %+v", out)
	}
}

// A reply the engine definitely refused was never going to be spoken.
func TestAReplyTheEngineRefusedIsNotOut(t *testing.T) {
	a := &App{cfg: &Config{}}
	h, f := speakerSession(t, a, "v")
	f.failInterrupt = errors.New("no admitted synthesis to fence")
	h.v = &fallbackAckEngine{fakeEngineSession: f,
		ackErr: pluginhosttest.Refusal("speech.session.synthesize", `{"code":-32000,"message":"not eligible"}`)}
	if got := a.synthesizeReply(t.Context(), h.id, 0, "refused"); got != replyRefusedDefinite {
		t.Fatalf("fixture refusal: %v", got)
	}
	if h.speaking() {
		t.Fatal("the engine refused the reply and the session says it is speaking")
	}
}

// The speak-off commit empties the in-flight record before its fence is
// sent. When the engine then refuses that fence the reply may still be
// speaking, and the record no longer names it: the session must go on
// saying so until the reply's own terminal report, which must end it
// although the record is empty.
func TestSpeakTurnedOffWithARefusedFenceLeavesTheReplyOutUntilItsTerminalReport(t *testing.T) {
	a := New(&Config{})
	defer a.bgCancel()
	on := VoiceModeConfig{Listen: listenInteractive, Speak: speakOn, Revision: 1}
	a.publishVoiceMode(on)
	h, f := speakerSession(t, a, "v")
	synth := admitSpeakerReply(t, a, h, f, 4, "a reply the off cannot stop")
	f.mu.Lock()
	f.failInterrupt = errors.New("fixture: stop refused")
	f.mu.Unlock()
	off := VoiceModeConfig{Listen: listenInteractive, Speak: speakOff, Revision: 2}
	a.publishVoiceMode(off)
	a.voiceModeCommitted(on, off)
	awaitAppWork(t, a)
	if got := h.loadInflight().id; got != "" {
		t.Fatalf("fixture: the commit left the in-flight record naming %q (want it emptied before the fence; reply %s)", got, synth)
	}
	if !h.speaking() {
		t.Fatal("the engine refused the off's fence, and the session says the reply is over")
	}
	playbackOf(t, h, 4, true)
	if h.speaking() {
		t.Fatal("the reply's terminal report did not end it once the in-flight record was already empty")
	}
}

// And when the engine admits the off's fence, the reply is over at once.
func TestSpeakTurnedOffEndsTheReplyWhenTheEngineAdmitsTheFence(t *testing.T) {
	a := New(&Config{})
	defer a.bgCancel()
	on := VoiceModeConfig{Listen: listenInteractive, Speak: speakOn, Revision: 1}
	a.publishVoiceMode(on)
	h, f := speakerSession(t, a, "v")
	admitSpeakerReply(t, a, h, f, 4, "a reply the off stops")
	off := VoiceModeConfig{Listen: listenInteractive, Speak: speakOff, Revision: 2}
	a.publishVoiceMode(off)
	a.voiceModeCommitted(on, off)
	awaitAppWork(t, a)
	if h.speaking() {
		t.Fatal("speak was turned off, the engine admitted the fence, and the session still says it is speaking")
	}
}

// A newer reply takes the older one's place: only the newer is out, and
// nothing the page still reports of the older one ends or renews it.
func TestAReplacedRepliesReportsNeitherEndNorRenewTheReplyThatReplacedIt(t *testing.T) {
	a := &App{cfg: &Config{}}
	h, f := speakerSession(t, a, "v")
	first := admitSpeakerReply(t, a, h, f, 1, "first")
	second := admitSpeakerReply(t, a, h, f, 2, "its replacement")
	if out := h.out.Load(); out == nil || out.id != second {
		t.Fatalf("after a replacement the reply out is %+v, want %s (the first was %s)", out, second, first)
	}
	// Age the newer reply to a second short of the bound: a report that
	// renewed it would be seen.
	aged := int64(voiceClock() - voiceSpeakingBound + time.Second)
	h.out.Load().heard.Store(aged)
	playbackOf(t, h, 1, false)
	if got := h.out.Load().heard.Load(); got != aged {
		t.Fatal("a progress report of the replaced reply renewed the reply that replaced it")
	}
	playbackOf(t, h, 1, true)
	if out := h.out.Load(); out == nil || out.id != second {
		t.Fatal("the replaced reply's terminal report ended the reply that replaced it")
	}
	playbackOf(t, h, 2, true)
	if h.speaking() {
		t.Fatal("the newer reply's own terminal report did not end it")
	}
}

// With no word of a reply for the bound, it is no longer out: a clear the
// host never received cannot hold whatever waits on speaking() for as long
// as a page stays open. Word of it (a progress report) renews it.
func TestAReplyLapsesAfterTheBoundWithNoWordOfItAndAProgressReportRenewsIt(t *testing.T) {
	a := &App{cfg: &Config{}}
	h, f := speakerSession(t, a, "v")
	before := voiceClock()
	admitSpeakerReply(t, a, h, f, 1, "a reply whose end the host is never told")
	admitted := time.Duration(h.out.Load().heard.Load())
	// The bound runs from the admission itself.
	if after := voiceClock(); admitted < before || admitted > after {
		t.Fatalf("the reply's admission is stamped %v, outside the moment it was admitted in (%v to %v)", admitted, before, after)
	}

	if voiceSpeakingBound != 2*time.Minute {
		t.Fatalf("the bound is %v; the ruling says two minutes", voiceSpeakingBound)
	}
	if !h.speakingAt(admitted + voiceSpeakingBound - time.Millisecond) {
		t.Fatal("the reply lapsed before the bound")
	}
	if h.speakingAt(admitted + voiceSpeakingBound) {
		t.Fatal("a reply with no word of it for the whole bound is still out")
	}
	// A second short of lapsing, the page reports progress.
	h.out.Load().heard.Store(int64(voiceClock() - voiceSpeakingBound + time.Second))
	if h.speakingAt(voiceClock() + 2*time.Second) {
		t.Fatal("fixture: the aged reply should lapse within two seconds")
	}
	playbackOf(t, h, 1, false)
	if !h.speakingAt(voiceClock() + 2*time.Second) {
		t.Fatal("a progress report did not renew the reply")
	}
	// A report of a stream the engine has not named is word of whatever
	// reply is out.
	h.out.Load().heard.Store(int64(voiceClock() - voiceSpeakingBound + time.Second))
	playbackOf(t, h, 7, false)
	if !h.speakingAt(voiceClock() + 2*time.Second) {
		t.Fatal("a progress report of an unnamed stream did not renew the reply that is out")
	}
}

// A session that is over is not speaking, whatever it had out.
func TestASessionThatEndedIsNotSpeaking(t *testing.T) {
	a := &App{cfg: &Config{}}
	h, f := speakerSession(t, a, "v")
	ended := make(chan struct{})
	h.done = ended
	admitSpeakerReply(t, a, h, f, 1, "cut off by the session's end")
	if !h.speaking() {
		t.Fatal("fixture: the reply is not out")
	}
	close(ended)
	if h.speaking() {
		t.Fatal("the session is over and still says it is speaking")
	}
}

// speaking is read by a loop that has nothing to do with voice, before
// every memory it embeds: it must never wait on the voice path. It
// answers while the session's admission lock is held, and it is read
// while replies are admitted, reported and fenced (the race detector is
// the judge of that half).
func TestSpeakingNeverWaitsOnTheVoicePath(t *testing.T) {
	a := &App{cfg: &Config{}}
	h, f := speakerSession(t, a, "v")
	admitSpeakerReply(t, a, h, f, 1, "a reply")
	h.admit.Lock()
	answered := make(chan bool, 1)
	go func() { answered <- h.speaking() }()
	select {
	case got := <-answered:
		if !got {
			t.Error("the reply is out and speaking said no")
		}
	case <-time.After(2 * time.Second):
		t.Error("speaking waited on the session's admission lock")
	}
	h.admit.Unlock()

	stop := make(chan struct{})
	var readers sync.WaitGroup
	defer func() { close(stop); readers.Wait() }()
	for i := 0; i < 4; i++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-stop:
					return
				default:
					_ = h.speaking()
				}
			}
		}()
	}
	for stream := uint32(10); stream < 60; stream++ {
		admitSpeakerReply(t, a, h, f, stream, "another reply")
		playbackOf(t, h, stream, false)
		if stream%2 == 0 {
			playbackOf(t, h, stream, true)
		} else if err := h.Interrupt(t.Context()); err != nil {
			t.Fatal(err)
		}
		if h.speaking() {
			t.Fatalf("stream %d: the reply ended and the session still says it is speaking", stream)
		}
	}
}
