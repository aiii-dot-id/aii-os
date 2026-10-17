package app

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"runtime/pprof"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/audio"
)

// heldCloseEngine is an engine whose close answers only when released, or
// when the context it was asked under ends.
type heldCloseEngine struct {
	*fakeEngineSession
	entered chan struct{}
	release chan struct{}
}

func (e *heldCloseEngine) CloseFor(ctx context.Context, sid, mode, reason string) error {
	close(e.entered)
	select {
	case <-e.release:
		return e.fakeEngineSession.CloseFor(ctx, sid, mode, reason)
	case <-ctx.Done():
		return ctx.Err()
	}
}

func heldSession(a *App, id string, contained bool) (*voiceHandle, *heldCloseEngine) {
	e := &heldCloseEngine{fakeEngineSession: &fakeEngineSession{}, entered: make(chan struct{}), release: make(chan struct{})}
	h := &voiceHandle{id: id, v: e, b: &audio.Binding{InputHandle: "in-" + id, Contained: contained}, done: make(chan struct{}), drained: make(chan struct{})}
	h.hush = func(why string) { a.hushFallback(h, why) }
	a.voiceSessions.Store(id, h)
	a.voiceModes.Store(id, true)
	return h, e
}

func awaitEngineAsked(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatalf("%s was never asked of the engine", what)
	}
}

// A DRAIN-CLOSE ENDS WITH THE IDENTITY (T19). The close that follows the
// input's completion is a control for a session that lives only while the
// identity does. Rooted on Background it held its own 10 s against an
// engine the plugin teardown was closing anyway; on the lifetime, the
// stop ends it.
func TestADrainCloseInFlightEndsWhenTheIdentityStops(t *testing.T) {
	a := newVoiceApp(t)
	h, e := heldSession(a, "vs-drain", true)
	a.voiceEngineEvent(nil, finishedEvent("vs-drain", 320), dropEvent)
	awaitEngineAsked(t, e.entered, "the drain-close")
	stopApp(t, a)
	select {
	case <-h.drained:
	case <-time.After(3 * time.Second):
		t.Fatal("the drain-close outlived the identity's stop: it waits out its own bound")
	}
	if c := e.closes(); len(c) != 0 {
		t.Fatalf("the engine never answered, so nothing was closed: %v", c)
	}
}

// stuckEngine answers a control only when released, or — like an engine
// whose plugin the teardown closed — fails it when closed. ignoreCtx makes
// it deaf to the control's own bound too: only the release or the close
// ends the wait.
type stuckEngine struct {
	*fakeEngineSession
	ignoreCtx bool
	asked     chan struct{}
	release   chan struct{}
	closed    chan struct{}
	askOnce   sync.Once
	closeOnce sync.Once
}

func newStuckEngine(ignoreCtx bool) *stuckEngine {
	return &stuckEngine{fakeEngineSession: &fakeEngineSession{}, ignoreCtx: ignoreCtx,
		asked: make(chan struct{}), release: make(chan struct{}), closed: make(chan struct{})}
}

var errEngineClosed = errors.New("the engine's plugin closed")

func (e *stuckEngine) wait(ctx context.Context) error {
	e.askOnce.Do(func() { close(e.asked) })
	done := ctx.Done()
	if e.ignoreCtx {
		done = nil
	}
	select {
	case <-e.release:
		return nil
	case <-e.closed:
		return errEngineClosed
	case <-done:
		return ctx.Err()
	}
}

func (e *stuckEngine) close() { e.closeOnce.Do(func() { close(e.closed) }) }

func (e *stuckEngine) CloseFor(ctx context.Context, sid, mode, reason string) error {
	if err := e.wait(ctx); err != nil {
		return err
	}
	return e.fakeEngineSession.CloseFor(ctx, sid, mode, reason)
}

func (e *stuckEngine) InterruptFor(ctx context.Context, sid, synthesisID, cause string) error {
	if err := e.wait(ctx); err != nil {
		return err
	}
	return e.fakeEngineSession.InterruptFor(ctx, sid, synthesisID, cause)
}

func stuckSession(a *App, id string, e *stuckEngine) *voiceHandle {
	h := &voiceHandle{id: id, v: e, b: &audio.Binding{InputHandle: "in-" + id}, done: make(chan struct{}), drained: make(chan struct{})}
	h.hush = func(why string) { a.hushFallback(h, why) }
	a.voiceSessions.Store(id, h)
	a.voiceModes.Store(id, true)
	return h
}

// stopAwaitsTheTurn says Shutdown's stop() is past its background join and
// waiting for the turn — the step before the plugin teardown.
func stopAwaitsTheTurn() bool {
	var buf bytes.Buffer
	_ = pprof.Lookup("goroutine").WriteTo(&buf, 2)
	for _, stack := range strings.Split(buf.String(), "\n\n") {
		if strings.Contains(stack, "internal/app.(*App).acquireTurn") && strings.Contains(stack, "internal/app.(*App).stop(") {
			return true
		}
	}
	return false
}

// A SAFE ABORT IS JOINED BY SHUTDOWN (T19). The abort is a bounded fence:
// nothing SAFE's entry carries may cut it short, so it keeps its own 10 s
// bound. It waits on the engine, so it is engine work: the teardown joins
// it after the plugins close, and a teardown that cannot (it outlived the
// join's bound) fails by its name. The engine here is deaf to the abort's
// own bound, so only the engine's answer — or the join's bound — ends the
// wait.
func TestASafeAbortInFlightIsJoinedByShutdown(t *testing.T) {
	t.Run("joined when the engine answers", func(t *testing.T) {
		a := newVoiceApp(t)
		e := newStuckEngine(true)
		stuckSession(a, "vs-safe", e) // not contained: SAFE aborts it
		a.abortVoiceSessionsUnderSafe("the test's SAFE")
		awaitEngineAsked(t, e.asked, "the SAFE abort")
		stopped := make(chan error, 1)
		go func() { stopped <- a.Shutdown(context.Background()) }()
		select {
		case err := <-stopped:
			close(e.release)
			t.Fatalf("Shutdown returned with the SAFE abort still in flight (%v): nothing joined it", err)
		case <-time.After(200 * time.Millisecond):
		}
		close(e.release)
		select {
		case err := <-stopped:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("Shutdown never returned after the abort did")
		}
		if c := e.closes(); len(c) != 1 || !strings.HasPrefix(c[0], "abort: SAFE: ") {
			t.Fatalf("the abort reached the engine, once: %v", c)
		}
	})
	t.Run("bounded and named when it never answers", func(t *testing.T) {
		// Not newVoiceApp: its cleanup fails a test whose teardown fails.
		a := New(&Config{SourcePath: filepath.Join(t.TempDir(), "config.json")})
		e := newStuckEngine(true)
		defer close(e.release)
		stuckSession(a, "vs-deaf", e)
		a.abortVoiceSessionsUnderSafe("the test's SAFE")
		awaitEngineAsked(t, e.asked, "the SAFE abort")
		began := time.Now()
		stopped := make(chan error, 1)
		go func() { stopped <- a.Shutdown(context.Background()) }()
		var err error
		select {
		case err = <-stopped:
		case <-time.After(engineJoinBound + 5*time.Second):
			t.Fatal("a SAFE abort the engine never answers held Shutdown past the join's bound")
		}
		if took := time.Since(began); took < engineJoinBound {
			t.Fatalf("Shutdown returned after %s, before the join's bound: the abort was not waited for", took)
		}
		if err == nil || !strings.Contains(err.Error(), "the SAFE abort of session vs-deaf") {
			t.Fatalf("a teardown that left the SAFE abort running returned %v — it must fail, naming it", err)
		}
	})
}

// A STUCK ENGINE CONTROL WAITS FOR THE PLUGIN TEARDOWN, NOT AHEAD OF IT
// (T19, the voice platform's measurement: Shutdown took 10.00 s with a SAFE
// abort stuck at the engine). A control that waits on an engine — SAFE's
// abort (its own 10 s), the fence of a reply another engine speaks (its
// own 5 s) — ends when the plugin teardown closes that engine. Under bgWG
// it was waited for BEFORE the teardown, for its whole bound. Here the
// turn is held, so stop() is caught at the step before the teardown: it
// must get there at once, and the engine closed there (as Deactivate
// closes it) ends the control, so Shutdown returns well under its bound.
func TestAStuckEngineControlDoesNotHoldShutdownAheadOfThePluginTeardown(t *testing.T) {
	for _, tc := range []struct {
		name  string
		start func(a *App, e *stuckEngine)
	}{
		{"SAFE's abort", func(a *App, e *stuckEngine) {
			stuckSession(a, "vs-safe", e)
			a.abortVoiceSessionsUnderSafe("the test's SAFE")
		}},
		{"the fence of a reply another engine speaks", func(a *App, e *stuckEngine) {
			on := stuckSession(a, "vs-out", e)
			a.hushTakenOver("vs-heard", takenOver{route: "engine", id: "syn-1", on: on}, "the operator spoke")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := newVoiceApp(t)
			e := newStuckEngine(false) // honours its own bound
			tc.start(a, e)
			awaitEngineAsked(t, e.asked, tc.name)
			if !a.TryBeginTurn() {
				t.Fatal("no turn claim")
			}
			turnHeld := true
			defer func() {
				if turnHeld {
					a.EndTurn()
				}
			}()
			began := time.Now()
			stopped := make(chan error, 1)
			go func() { stopped <- a.Shutdown(context.Background()) }()
			deadline := time.Now().Add(3 * time.Second)
			for !stopAwaitsTheTurn() {
				if time.Now().After(deadline) {
					e.close()
					t.Fatalf("Shutdown waited on %s at the engine before the plugin teardown could close it", tc.name)
				}
				time.Sleep(5 * time.Millisecond)
			}
			e.close() // the plugin teardown closes the engine
			turnHeld = false
			a.EndTurn()
			select {
			case err := <-stopped:
				if err != nil {
					t.Fatalf("teardown: %v", err)
				}
			case <-time.After(3 * time.Second):
				t.Fatalf("Shutdown did not return once the engine was closed: %s still held it", tc.name)
			}
			if took := time.Since(began); took > 3*time.Second {
				t.Fatalf("Shutdown took %s with %s stuck at a closed engine", took, tc.name)
			}
		})
	}
}

// DELIVERED WORDS ARE RECORDED WHATEVER ENDED (T19). Words steered into a
// running turn reach the model at its next tool boundary; their record is
// written there, under the arrival's own context. That context may have
// ended by then — the page that sent them gone, the identity stopping
// under the turn — and the words still reach the model, so their record
// must complete too.
func TestSteeredWordsAreRecordedAfterTheirArrivalEnded(t *testing.T) {
	a := newVoiceApp(t)
	if !a.TryBeginTurn() {
		t.Fatal("no turn claim")
	}
	defer a.EndTurn()
	ended, end := context.WithCancel(context.Background())
	end()
	if steered, err := a.steer(roleOperator, "the file is in the other folder", nil, ended); err != nil || !steered {
		t.Fatalf("not steered: %v %v", steered, err)
	}
	if said := a.DrainSteering(); len(said) != 1 {
		t.Fatalf("the words were not delivered: %v", said)
	}
	turns, err := a.store.RecentTurns(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) != 1 || !strings.Contains(turns[0].Content, "the file is in the other folder") {
		t.Fatalf("the model read words the record does not hold: %+v", turns)
	}
}
