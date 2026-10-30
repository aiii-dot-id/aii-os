//go:build (linux && !android) || (darwin && !ios) || windows

// The supervised lane and the native session engine run where a plugin's
// child is supervised: the desktop systems (the suite's other supervised-lane
// tests carry the same constraint).

package pluginhost

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/supervisor"
)

// HOW A CHILD IS KEPT IS THE OPERATOR'S (plugins.runtime.stop_ms,
// hand_over_ms, session_control_ms, restart_attempts; child_bounds.go).
// Each bound here was a constant for every plugin; each test sets the
// operator's figure well apart from the default, so the test fails when the
// figure is not read and the default is used instead.

// wasmWorker stages a WASM package and starts its supervised lane with the
// suite's fakechild as the worker, in mode, under opts' bounds.
func wasmWorker(t *testing.T, id, mode string, bounds ChildBounds) *ActivePlugin {
	t.Helper()
	account := []byte(`[{"id":"ping","summary":"answers"}]`)
	pkg := describingPkg(t, id, account, account)
	opts := &Options{WorkerBinary: fakechildBin, WorkerArgs: []string{mode}, Bounds: bounds}
	st, err := Stage(context.Background(), pkg, opts)
	if err != nil {
		t.Fatal(err)
	}
	ap := &ActivePlugin{ID: id}
	if _, err := st.startWASMSupervised(context.Background(), ap, nil, ""); err != nil {
		t.Fatalf("the worker reports ready and is started: %v", err)
	}
	return ap
}

// A CHILD'S RESTARTS ARE ITS OPERATOR'S. Its supervisor counted five lives
// in ten minutes for every plugin. A worker that dies each time it is ready,
// given two, is restarted twice and then stopped with the ceiling's reason.
func TestAChildsRestartsAreItsOperatorsFigure(t *testing.T) {
	ap := wasmWorker(t, "org.example.bounds-restarts", "ready-crash", ChildBounds{RestartAttempts: 2})
	t.Cleanup(func() { _ = ap.closeChannel(context.Background()) })
	deadline := time.Now().Add(30 * time.Second)
	for {
		st, err := ap.sup.State()
		if st == supervisor.StateStopped {
			var ceiling *supervisor.RestartCeilingError
			if got := ap.sup.Restarts(); got != 2 || !errors.As(err, &ceiling) {
				t.Fatalf("a child given two restarts was restarted %d times and stopped with %v; want 2, at the ceiling (the default is %d)", got, err, supervisor.DefaultMaxRestarts)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("a child that dies at every start was still kept after 30s: %d restarts", ap.sup.Restarts())
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// A SWITCH WAITS THE OPERATOR'S HAND-OVER. A predecessor busy with a call
// held a switch for thirty seconds on every machine; the hand-over is read
// from the options when the switch is made. Given 300 ms, a switch whose
// predecessor stays busy is refused within the second, and the predecessor
// keeps serving.
func TestASwitchWaitsTheOperatorsHandOver(t *testing.T) {
	reg := newRegistry(t)
	old, err := Activate(context.Background(), buildPkg(t, "org.example.bounds-handover", "0.1.0", []string{"ping"}, "responder.wasm"), reg, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = old.Deactivate(context.Background()) })
	next, err := ActivateShadow(context.Background(), buildPkg(t, "org.example.bounds-handover", "0.2.0", []string{"ping"}, "responder.wasm"), reg, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = next.Deactivate(context.Background()) })
	opts := &Options{Bounds: ChildBounds{HandOver: 300 * time.Millisecond}}
	rt := NewRuntime(RuntimeWires{Options: func() *Options { return opts }})
	if err := old.inFlight.acquire(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer old.inFlight.release()
	switched := make(chan error, 1)
	began := time.Now()
	go func() {
		switched <- rt.Redirect(&Activation{ID: old.ID, Plugin: old}, &Activation{ID: next.ID, Plugin: next})
	}()
	select {
	case err := <-switched:
		took := time.Since(began)
		if !errors.Is(err, ErrHandOverBusy) || took < 250*time.Millisecond {
			t.Fatalf("a switch given 300ms, its predecessor busy: %v after %s", err, took)
		}
		if next.admitted.Load() || old.superseded.Load() {
			t.Fatal("the refused switch moved the names")
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("a switch given 300ms still waited after 5s: the default's %s, not the operator's", DefaultHandOverBudget)
	}
}

// A CHECK'S SESSION IS KEPT BY THE BOUNDS ITS ACTIVATION STARTED UNDER. A
// resident engine's publisher's check waited five seconds for each control
// and five for the end of a session it aborted, whatever the machine. An
// engine that never admits the open, given one second for a control, ends
// its check within two; one that admits the abort and never ends the
// session, given one second to stop, is refused within two.
func TestACheckIsKeptByTheBoundsItsActivationStartedUnder(t *testing.T) {
	for _, c := range []struct {
		name, mode string
		bounds     ChildBounds
	}{
		{"an open never admitted, a control bound of 1s", "session-check-mute", ChildBounds{SessionControl: time.Second}},
		{"a session never ended, a stop of 1s", "session-check-noend", ChildBounds{Stop: time.Second}},
	} {
		t.Run(c.name, func(t *testing.T) {
			pkg, opts := sessionEngine(t, "org.example.bounds-"+c.mode, c.mode, checksOf(speaksCheck("speaks", 5000)))
			opts.Bounds = c.bounds
			ap, err := ActivateShadow(t.Context(), pkg, newRegistry(t), opts)
			if err != nil {
				t.Fatalf("activate the candidate: %v", err)
			}
			act := &Activation{ID: ap.ID, Plugin: ap}
			rt := NewRuntime(RuntimeWires{Options: func() *Options { return opts }, Pinned: func(*Activation, *VoiceSession) {}})
			t.Cleanup(func() { _, _ = rt.Stop(context.Background(), act) })
			began := time.Now()
			checks, err := rt.Validate(t.Context(), act)
			took := time.Since(began)
			if checks.Failed == nil && err == nil {
				t.Fatalf("the check passed: %+v", checks)
			}
			var unended *SessionUnendedError
			if c.mode == "session-check-noend" && !errors.As(err, &unended) {
				t.Fatalf("an unended session is the typed refusal: %v", err)
			}
			if took < 900*time.Millisecond || took >= 2*time.Second+500*time.Millisecond {
				t.Fatalf("the check ended after %s: want its activation's 1s, not the default's %s", took, DefaultSessionControlBound)
			}
		})
	}
}

// nativeChild activates a signed native package whose child is the suite's
// fakechild in mode, under bounds.
func nativeChild(t *testing.T, id, mode string, bounds ChildBounds) *ActivePlugin {
	t.Helper()
	skipWhereNativeIsRefused(t)
	skipWhereTheSandboxCannotBeEstablished(t)
	roots, role := sharedPlatformRoots(t)
	child, err := os.ReadFile(fakechildBin)
	if err != nil {
		t.Fatal(err)
	}
	model := modeModel(t, mode)
	models, err := json.Marshal([]ModelDecl{model.decl})
	if err != nil {
		t.Fatal(err)
	}
	pkg := nativePackageAs(t, id, child, role, map[string][]byte{ModelsFile: models}, nil, nil)
	opts := supervisedOpts(Options{Roots: roots, Bounds: bounds})
	useStore(t, opts, model.fetch)
	ap, err := Activate(context.Background(), pkg, newRegistry(t), opts)
	if err != nil {
		t.Fatalf("activate: %v", err)
	}
	return ap
}

// A NATIVE CHILD'S RESTARTS ARE ITS OPERATOR'S TOO: the native lane's
// supervisor is handed the same figure as the WASM worker's. A child that
// dies after each start, given two, is restarted twice and stopped.
func TestANativeChildsRestartsAreItsOperatorsFigure(t *testing.T) {
	ap := nativeChild(t, "org.example.bounds-native-restarts", "crash", ChildBounds{RestartAttempts: 2})
	t.Cleanup(func() { _ = ap.CloseQuiet(context.Background()) })
	deadline := time.Now().Add(30 * time.Second)
	for {
		st, err := ap.sup.State()
		if st == supervisor.StateStopped {
			var ceiling *supervisor.RestartCeilingError
			if got := ap.sup.Restarts(); got != 2 || !errors.As(err, &ceiling) {
				t.Fatalf("a native child given two restarts was restarted %d times and stopped with %v; want 2, at the ceiling", got, err)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("a native child that dies at every start was still kept after 30s: %d restarts", ap.sup.Restarts())
		}
		time.Sleep(20 * time.Millisecond)
	}
}
