package app

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost/pluginhosttest"
)

type heldReplyFence struct {
	*fakeEngineSession
	first   atomic.Bool
	entered chan string
	resume  chan struct{}
}

func (f *heldReplyFence) InterruptFor(ctx context.Context, sid, synth, why string) error {
	if f.first.CompareAndSwap(false, true) {
		f.entered <- synth
		select {
		case <-f.resume:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return f.fakeEngineSession.InterruptFor(ctx, sid, synth, why)
}

func TestAReplacementFenceDoesNotHoldStopOrAdmitASupersededReply(t *testing.T) {
	a := &App{cfg: &Config{}}
	f := &heldReplyFence{fakeEngineSession: &fakeEngineSession{}, entered: make(chan string, 1), resume: make(chan struct{})}
	h := &voiceHandle{id: "v", v: f}
	a.voiceSessions.Store(h.id, h)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if a.synthesizeReply(ctx, h.id, 0, "first") != replyAdmitted {
		t.Fatal("first reply not admitted")
	}
	prior := h.loadInflight().id
	var once sync.Once
	resume := func() { once.Do(func() { close(f.resume) }) }
	defer resume()
	done := make(chan replyVerdict, 1)
	go func() { done <- a.synthesizeReply(ctx, h.id, 0, "replacement") }()
	select {
	case id := <-f.entered:
		if id != prior {
			t.Fatalf("replacement fenced %q instead of %q", id, prior)
		}
	case <-ctx.Done():
		t.Fatal("replacement never fenced the predecessor")
	}
	// The old fence's acknowledgement is withheld. Stop must still enter
	// through admit, name the same reply and move the pending generation.
	interrupted := make(chan error, 1)
	go func() { interrupted <- h.Interrupt(ctx) }()
	select {
	case err := <-interrupted:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("stop was held behind the replacement's acknowledgement")
	}
	resume()
	select {
	case got := <-done:
		if got != replySuperseded || len(f.synthed()) != 1 {
			t.Fatalf("a stopped replacement reached synthesis: %v %v", got, f.synthed())
		}
	case <-ctx.Done():
		t.Fatal("stop was held behind the replacement's acknowledgement")
	}
	if a.synthesizeReply(ctx, h.id, h.gen.Load(), "recovery") != replyAdmitted {
		t.Fatal("a fresh reply did not recover after the stop")
	}
}

func TestABufferedReplyIsNamedByManualAndObservedSpeechStops(t *testing.T) {
	for _, speech := range []bool{false, true} {
		t.Run(map[bool]string{false: "manual", true: "observed-speech"}[speech], func(t *testing.T) {
			a := New(&Config{})
			defer a.bgCancel()
			f := &fakeEngineSession{}
			h := &voiceHandle{id: "v", v: f}
			h.setInflight("production-ended", 0, nil)
			a.voiceSessions.Store(h.id, h)
			if speech {
				a.voiceObserved(decodeVoiceFrame(pluginhost.Event{Type: "speech_start", SessionID: h.id}))
				a.bgWG.Wait()
			} else if err := h.Interrupt(t.Context()); err != nil {
				t.Fatal(err)
			}
			if got := strings.Join(f.opsSeen(), ","); got != "interrupt:production-ended:v" {
				t.Fatalf("stop failed to name buffered audio after production ended: %s", got)
			}
			if h.loadInflight().id != "production-ended" {
				t.Fatal("a stop request manufactured a terminal receipt")
			}
		})
	}
}

func TestTheObservedSpeechFenceIsOwnedAndCancelledByTheApp(t *testing.T) {
	a := New(&Config{})
	f := &heldReplyFence{fakeEngineSession: &fakeEngineSession{}, entered: make(chan string, 1), resume: make(chan struct{})}
	h := &voiceHandle{id: "v", v: f}
	h.setInflight("buffered", 0, nil)
	a.voiceSessions.Store(h.id, h)
	defer a.bgCancel()
	a.voiceObserved(decodeVoiceFrame(pluginhost.Event{Type: "speech_start", SessionID: h.id}))
	select {
	case id := <-f.entered:
		if id != "buffered" {
			t.Fatalf("observed speech fenced %q", id)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the observed speech fence was not dispatched")
	}
	// The acknowledgement remains withheld. Cancellation of the existing
	// App owner, not that acknowledgement or a separate timeout, must join it.
	a.bgCancel()
	done := make(chan struct{})
	go func() { a.bgWG.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("the speech fence escaped App cancellation")
	}
	if h.loadInflight().id != "buffered" {
		t.Fatal("a cancelled fence discarded unresolved playback custody")
	}
}

func TestARefusedReplacementFenceKeepsCustodyAndCanRecover(t *testing.T) {
	a := &App{cfg: &Config{}}
	f := &fakeEngineSession{}
	h := &voiceHandle{id: "v", v: f}
	a.voiceSessions.Store(h.id, h)
	ctx := t.Context()
	if a.synthesizeReply(ctx, h.id, 0, "first") != replyAdmitted {
		t.Fatal("first reply not admitted")
	}
	prior := h.loadInflight().id
	f.failInterrupt = errors.New("fixture: stop refused")
	if got := a.synthesizeReply(ctx, h.id, 0, "replacement"); got != replySuperseded {
		t.Fatalf("a refused predecessor fence admitted a replacement: %v", got)
	}
	if h.loadInflight().id != prior || len(f.synthed()) != 1 {
		t.Fatal("failed fencing lost the predecessor or dispatched another synthesis")
	}
	if why, _ := h.replyOutcome.Load().(string); !strings.Contains(why, "did NOT fence") {
		t.Fatalf("fence refusal was hidden: %s", why)
	}
	f.failInterrupt = nil
	if a.synthesizeReply(ctx, h.id, 0, "recovered") != replyAdmitted || len(f.synthed()) != 2 {
		t.Fatal("the same session did not recover when its fence succeeded")
	}
}

func TestADefinitelyRefusedSynthesisDoesNotBlockTheNextReply(t *testing.T) {
	a := &App{cfg: &Config{}}
	f := &fakeEngineSession{failInterrupt: errors.New("no admitted synthesis to fence")}
	h := &voiceHandle{id: "v", v: &fallbackAckEngine{fakeEngineSession: f,
		ackErr: pluginhosttest.Refusal("speech.session.synthesize", `{"code":-32000,"message":"not eligible"}`)}}
	a.voiceSessions.Store(h.id, h)
	if got := a.synthesizeReply(t.Context(), h.id, 0, "refused"); got != replyRefusedDefinite {
		t.Fatalf("fixture refusal: %v", got)
	}
	if h.loadInflight().id != "" {
		t.Fatal("a definitely refused synthesis retained playback custody")
	}
	h.v = f
	if a.synthesizeReply(t.Context(), h.id, 0, "now eligible") != replyAdmitted {
		t.Fatal("a nonexistent predecessor blocked a usable engine")
	}
}
