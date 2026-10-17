package app

import (
	"context"
	"errors"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/audio"
	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost/pluginhosttest"
)

// failingEngine answers every operator control with one error.
type failingEngine struct {
	*fakeEngineSession
	err error
}

func (f failingEngine) FinishInputFor(context.Context, string, string, int64) error { return f.err }
func (f failingEngine) CloseFor(context.Context, string, string, string) error      { return f.err }
func (f failingEngine) InterruptFor(context.Context, string, string, string) error  { return f.err }

// THE ENGINE NOT BEING THERE IS MARKED, NEVER READ FROM WORDING (T74). The
// dashboard tells the operator "Voice engine restarting." for a control the
// engine was not there to answer, and the control's own sentence for
// anything else; it decides by errors.Is alone. So the handle marks a
// control that never reached the engine, or a dead transport, with
// dashboard.ErrVoiceEngineUnavailable — on finish, interrupt and close —
// keeps the driver's error in the chain, and leaves the engine's own
// refusal and an unknown admission unmarked.
func TestAControlTheEngineWasNotThereForIsMarkedUnavailable(t *testing.T) {
	refused := pluginhosttest.Refusal("speech.session.stop_playback", `{"code":-32000,"message":"STALE_SYNTHESIS"}`)
	for _, c := range []struct {
		name string
		err  error
		gone bool
	}{
		{"the request never reached the engine", pluginhost.ErrNotAdmitted, true},
		{"the transport is dead", pluginhost.ErrSessionDead, true},
		{"the engine refused", refused, false},
		{"the admission is unknown", pluginhosttest.ErrAdmissionUnknown, false},
	} {
		h := &voiceHandle{id: "vs-t74", v: failingEngine{&fakeEngineSession{}, c.err}, b: &audio.Binding{InputHandle: "in-vs-t74", Contained: true}, done: make(chan struct{}), drained: make(chan struct{})}
		for control, err := range map[string]error{
			"finish":    h.Finish(context.Background(), 4800),
			"interrupt": h.Interrupt(context.Background()),
			"close":     h.Close(context.Background(), "abort"),
		} {
			if got := errors.Is(err, dashboard.ErrVoiceEngineUnavailable); got != c.gone {
				t.Errorf("%s, %s: marked unavailable = %v, want %v (%v)", c.name, control, got, c.gone, err)
			}
			if !errors.Is(err, c.err) {
				t.Errorf("%s, %s: the driver's error left the chain: %v", c.name, control, err)
			}
		}
	}
}

func TestTheEnginesRefusalOfAnOpenIsMarkedAsTheEngines(t *testing.T) {
	refused := pluginhosttest.Refusal("speech.session.open", `{"code":-32002,"message":"BUSY"}`)
	for _, c := range []struct {
		name          string
		err           error
		refused, gone bool
	}{
		{"the engine refused", refused, true, false},
		{"the admission is unknown", pluginhosttest.ErrAdmissionUnknown, true, false},
		{"the request never reached the engine", pluginhost.ErrNotAdmitted, false, true},
		{"the transport is dead", pluginhost.ErrSessionDead, false, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			a := speechRouteApp(t, "id.engine.one", "id.engine.one")
			runningEngine(t, a, "id.engine.one")
			prev := voiceEngineOpen
			t.Cleanup(func() { voiceEngineOpen = prev })
			voiceEngineOpen = func(*pluginhost.VoiceSession, context.Context, string, *audio.Binding, map[string]any) error {
				return c.err
			}
			_, err := openHearing(a)
			if errors.Is(err, dashboard.ErrVoiceEngineRefused) != c.refused || errors.Is(err, dashboard.ErrVoiceEngineUnavailable) != c.gone {
				t.Fatalf("marked refused = %v, unavailable = %v; want %v, %v (%v)", errors.Is(err, dashboard.ErrVoiceEngineRefused), errors.Is(err, dashboard.ErrVoiceEngineUnavailable), c.refused, c.gone, err)
			}
			if !errors.Is(err, c.err) {
				t.Fatalf("the driver's error left the chain: %v", err)
			}
		})
	}
	t.Run("the host refused before asking the engine", func(t *testing.T) {
		a := speechRouteApp(t, "id.engine.absent", "id.engine.absent")
		runningEngine(t, a, "id.engine.one")
		_, err := openHearing(a)
		if err == nil || errors.Is(err, dashboard.ErrVoiceEngineRefused) || errors.Is(err, dashboard.ErrVoiceEngineUnavailable) {
			t.Fatalf("a host refusal must pass unmarked: %v", err)
		}
	})
}
