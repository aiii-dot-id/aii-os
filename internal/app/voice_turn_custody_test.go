package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
)

// .
// .
// .
// .
// .
// .
// .
// .
func TestBargeInIsNotHeldBehindADelayedAcknowledgement(t *testing.T) {
	a := &App{cfg: &Config{}}
	f := &fakeEngineSession{entered: make(chan struct{}), ackGate: make(chan struct{})}
	id := "vs-1"
	h := &voiceHandle{id: id, v: f, done: make(chan struct{}), drained: make(chan struct{})}
	a.voiceSessions.Store(id, h)
	var shown, refused []string
	a.voiceReplySink = func(_ dashboard.VoiceReplyRef, text string) { shown = append(shown, text) }
	a.voiceEventSink = func(ev dashboard.VoiceEvent) {
		if ev.Type == "reply_refused" {
			refused = append(refused, ev.Reason)
		}
	}
	ctx := context.Background()
	gen := a.voiceGen(id)
	done := make(chan struct{})
	go func() { a.synthesizeReply(ctx, id, gen, "the stale answer"); close(done) }()
	<-f.entered
	stale := f.producingNow()
	if stale == "" {
		t.Fatal("the engine produces audio from the enqueue, before any acknowledgement")
	}
	intDone := make(chan error, 1)
	go func() { intDone <- h.Interrupt(ctx) }()
	select {
	case err := <-intDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the barge-in waited behind the withheld synthesis acknowledgement")
	}
	if f.producingNow() != "" {
		t.Fatalf("the barge-in must stop the active production while the acknowledgement is still withheld: still making %s", f.producingNow())
	}
	close(f.ackGate)
	<-done
	if len(shown) != 0 {
		t.Fatalf("the stale answer is never shown as spoken: %v", shown)
	}
	if len(refused) != 1 || !strings.Contains(refused[0], "superseded") {
		t.Fatalf("the stale answer is refused as superseded: %v", refused)
	}
	if got := strings.Join(f.opsSeen(), ","); got != "synthesize:vs-1,interrupt::vs-1,interrupt:"+stale+":vs-1" {
		t.Fatalf("the barge-in follows the enqueue and names the current generation; the by-id fence follows: %s", got)
	}
	// .
	f.mu.Lock()
	f.ackGate = nil
	f.mu.Unlock()
	a.synthesizeReply(ctx, id, a.voiceGen(id), "the recovery answer")
	if got := f.synthed(); len(got) != 2 || !strings.HasSuffix(got[1], "|the recovery answer") {
		t.Fatalf("the recovery answer is admitted: %v", got)
	}
	if f.producingNow() == "" || f.producingNow() == stale {
		t.Fatalf("the recovery answer is what the engine produces now: %q", f.producingNow())
	}
	if len(shown) != 1 {
		t.Fatalf("the recovery answer is shown once: %v", shown)
	}
}

// .
// .
// .
func TestBargeInBeforeAdmissionRefusesTheReplyBeforeDispatch(t *testing.T) {
	a := &App{cfg: &Config{}}
	f := &fakeEngineSession{}
	id := "vs-1"
	h := &voiceHandle{id: id, v: f, done: make(chan struct{})}
	a.voiceSessions.Store(id, h)
	var refused []string
	a.voiceEventSink = func(ev dashboard.VoiceEvent) {
		if ev.Type == "reply_refused" {
			refused = append(refused, ev.Reason)
		}
	}
	ctx := context.Background()
	gen := a.voiceGen(id)
	if err := h.Interrupt(ctx); err != nil {
		t.Fatal(err)
	}
	a.synthesizeReply(ctx, id, gen, "the stale answer")
	if got := strings.Join(f.opsSeen(), ","); got != "interrupt::vs-1" {
		t.Fatalf("nothing is enqueued for a superseded reply: %s", got)
	}
	if len(refused) != 1 {
		t.Fatalf("refused once, as evidence: %v", refused)
	}
	a.synthesizeReply(ctx, id, a.voiceGen(id), "the fresh answer")
	if got := f.synthed(); len(got) != 1 || !strings.HasSuffix(got[0], "|the fresh answer") {
		t.Fatalf("a fresh turn produces exactly one synthesis: %v", got)
	}
}

// .
// .
// .
// .
// .
// .
// .
func TestEngineSpeechSupersedesPendingRepliesButNotTheDrain(t *testing.T) {
	a := newVoiceApp(t)
	id := "vs-1"
	h, f := newTrackedSession(a, id, true)
	var refused []string
	a.voiceEventSink = func(ev dashboard.VoiceEvent) {
		if ev.Type == "reply_refused" {
			refused = append(refused, ev.Reason)
		}
	}
	ctx := context.Background()

	gen := a.voiceGen(id)
	a.voiceObserved(pluginhost.Event{Type: "speech_start", SessionID: id})
	a.synthesizeReply(ctx, id, gen, "an answer to what was said before")
	gen = a.voiceGen(id)
	a.voiceObserved(pluginhost.Event{Type: "interruption_requested", SessionID: id})
	a.synthesizeReply(ctx, id, gen, "still stale")
	if len(f.synthed()) != 0 || len(refused) != 2 {
		t.Fatalf("speech supersedes pending replies in the observer: synth=%v refused=%v", f.synthed(), refused)
	}
	a.voiceObserved(pluginhost.Event{Type: "transcript_partial", SessionID: id})
	a.voiceObserved(pluginhost.Event{Type: "speech_start", SessionID: "vs-other"})
	a.synthesizeReply(ctx, id, a.voiceGen(id), "fresh")
	if got := f.synthed(); len(got) != 1 {
		t.Fatalf("a partial, or another session's speech, moves nothing: %v", got)
	}

	// .
	// .
	stubWake(t, func() (string, error) {
		a.voiceObserved(pluginhost.Event{Type: "speech_start", SessionID: id})
		return "the final answer", nil
	})
	if err := h.Finish(ctx, 320); err != nil {
		t.Fatal(err)
	}
	a.voiceEngineEvent(nil, finalEvent(id, "goodbye"), dropEvent)
	a.voiceEngineEvent(nil, finishedEvent(id, 320), dropEvent)
	awaitDrained(t, h)
	if got := f.synthed(); len(got) != 1 {
		t.Fatalf("the superseded final reply is refused: %v", got)
	}
	if c := f.closes(); len(c) != 1 || !strings.HasPrefix(c[0], "drain: reply refused") {
		t.Fatalf("speech is not a close: the completion still drains, naming the refusal: %v", c)
	}
}
