package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/audio"
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
func TestReplyBoundToSessionAndTurnRefusesStaleAnswers(t *testing.T) {
	a := &App{cfg: &Config{}}
	f := &fakeEngineSession{}
	id := "vs-1"
	h := &voiceHandle{id: id, v: f, done: make(chan struct{})}
	a.voiceSessions.Store(id, h)
	var refused, shown []string
	a.voiceEventSink = func(ev dashboard.VoiceEvent) {
		if ev.Type == "reply_refused" {
			refused = append(refused, ev.Reason)
		}
	}
	a.voiceReplySink = func(ref dashboard.VoiceReplyRef, text string) {
		shown = append(shown, ref.Route+"|"+ref.SessionID+"|"+text)
	}
	ctx := context.Background()

	// .
	// .
	gen := a.voiceGen(id)
	if err := h.Interrupt(ctx); err != nil {
		t.Fatal(err)
	}
	a.synthesizeReply(ctx, id, gen, "the old answer")
	if n := len(f.synthed()); n != 0 {
		t.Fatalf("a delayed answer released after a barge-in must not play: %v", f.synthed())
	}
	if len(refused) != 1 || !strings.Contains(refused[0], "superseded") {
		t.Fatalf("the refusal is reported as evidence: %v", refused)
	}
	if len(shown) != 0 {
		t.Fatalf("a refused reply must not reach the page as a speakable response: %v", shown)
	}

	// .
	// .
	gen2 := a.voiceGen(id)
	a.synthesizeReply(ctx, id, gen2, "the fresh answer")
	if got := f.synthed(); len(got) != 1 || !strings.Contains(got[0], "the fresh answer") {
		t.Fatalf("a fresh turn produces exactly one synthesis: %v", got)
	}
	if len(shown) != 1 || !strings.HasPrefix(shown[0], "plugin|"+id+"|the fresh answer") {
		t.Fatalf("an admitted reply reaches the page with plugin provenance: %v", shown)
	}

	// .
	gen3 := a.voiceGen(id)
	if err := h.Close(ctx, "abort"); err != nil {
		t.Fatal(err)
	}
	a.synthesizeReply(ctx, id, gen3, "an answer pending across the close")
	if len(f.synthed()) != 1 {
		t.Fatalf("an answer pending across a close must not play: %v", f.synthed())
	}

	// .
	// .
	gen4 := a.voiceGen(id)
	a.voiceSessions.Delete(id)
	a.synthesizeReply(ctx, id, gen4, "an answer after the session ended")
	if len(f.synthed()) != 1 {
		t.Fatalf("an answer after the session ended must not play: %v", f.synthed())
	}
	if len(refused) != 3 {
		t.Fatalf("each refusal is evidence: %v", refused)
	}

	// .
	a.synthesizeReply(ctx, id, 0, "")
	a.synthesizeReply(ctx, "", 0, "x")
	if len(refused) != 3 || len(f.synthed()) != 1 {
		t.Fatalf("empty reply/session is a no-op: refused=%v synthed=%v", refused, f.synthed())
	}
}

// .
// .
// .
// .
func TestOldHandleCannotReachAReplacementSession(t *testing.T) {
	f := &fakeEngineSession{current: "vs-2"}
	old := &voiceHandle{id: "vs-1", v: f, b: &audio.Binding{InputHandle: "in-1"}, done: make(chan struct{})}
	ctx := context.Background()
	for name, err := range map[string]error{
		"finish":    old.Finish(ctx, 320),
		"interrupt": old.Interrupt(ctx),
		"close":     old.Close(ctx, "abort"),
	} {
		if !errors.Is(err, pluginhost.ErrStaleSession) {
			t.Fatalf("%s on an old handle must be refused as stale, got %v", name, err)
		}
	}
	a := &App{cfg: &Config{}}
	a.voiceSessions.Store("vs-1", old)
	a.synthesizeReply(ctx, "vs-1", old.gen.Load(), "into the wrong session")
	if len(f.synthed()) != 0 {
		t.Fatalf("a stale handle's reply must not migrate into the replacement session: %v", f.synthed())
	}
	if len(f.closes()) != 0 {
		t.Fatalf("no close from the old handle reached the replacement session: %v", f.closes())
	}
	// .
	cur := &voiceHandle{id: "vs-2", v: f, b: &audio.Binding{InputHandle: "in-2"}, done: make(chan struct{})}
	if err := cur.Interrupt(ctx); err != nil {
		t.Fatalf("the current session's handle is served: %v", err)
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
func TestInterruptRacingAdmissionFencesTheAdmittedReply(t *testing.T) {
	race := func(t *testing.T, id string, f *fakeEngineSession) (synthID string, refused, shown []string) {
		a := &App{cfg: &Config{}}
		h := &voiceHandle{id: id, v: f, done: make(chan struct{})}
		a.voiceSessions.Store(id, h)
		a.voiceEventSink = func(ev dashboard.VoiceEvent) {
			if ev.Type == "reply_refused" {
				refused = append(refused, ev.Reason)
			}
		}
		a.voiceReplySink = func(_ dashboard.VoiceReplyRef, text string) { shown = append(shown, text) }
		f.onSynthesize = func() { a.voiceObserved(pluginhost.Event{Type: "speech_start", SessionID: id}) }
		gen := a.voiceGen(id)
		a.synthesizeReply(context.Background(), id, gen, "the raced answer")
		got := f.synthed()
		if len(got) != 1 {
			t.Fatalf("the engine admitted the synthesis before the speech was seen: %v", got)
		}
		synthID = strings.SplitN(got[0], "|", 2)[0]
		fenced := false
		for _, op := range f.opsSeen() {
			if op == "interrupt:"+synthID+":"+id {
				fenced = true
			}
		}
		if !fenced {
			t.Fatalf("the superseded synthesis must be fenced by its own id: %v", f.opsSeen())
		}
		if len(shown) != 0 {
			t.Fatalf("a superseded reply must not be shown as spoken: %v", shown)
		}
		if len(refused) != 1 {
			t.Fatalf("the race is reported as evidence, once: %v", refused)
		}
		if !strings.Contains(refused[0], "superseded while its admission was pending") {
			t.Fatalf("the refusal names the supersession: %q", refused[0])
		}
		return synthID, refused, shown
	}

	f := &fakeEngineSession{}
	synthID, refused, _ := race(t, "vs-1", f)
	if f.producingNow() != "" {
		t.Fatalf("the fence must stop the admitted synthesis's production: still making %s", f.producingNow())
	}
	if !strings.Contains(refused[0], synthID+" was fenced") {
		t.Fatalf("the refusal names the fence that happened: %q", refused[0])
	}

	// .
	// .
	f2 := &fakeEngineSession{failInterrupt: errors.New("STALE_SYNTHESIS")}
	synthID2, refused2, _ := race(t, "vs-2", f2)
	if f2.producingNow() != synthID2 {
		t.Fatalf("a refused fence leaves the synthesis producing: %q", f2.producingNow())
	}
	if !strings.Contains(refused2[0], "did NOT fence synthesis "+synthID2) || strings.Contains(refused2[0], "was fenced") || strings.Contains(refused2[0], "cancelled") {
		t.Fatalf("a refused fence is reported as refused, never as a cancellation: %q", refused2[0])
	}
}
