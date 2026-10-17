package app

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/audio"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
	"github.com/aiii-dot-id/aii-os/internal/tools"
)

// endsWhenAborted is a driver whose session ends on an abort, as an engine's
// does once it has said so; one with ends nil never ends.
type endsWhenAborted struct {
	*fakeEngineSession
	once sync.Once
	ends chan struct{}
}

func (e *endsWhenAborted) CloseFor(ctx context.Context, sid, mode, reason string) error {
	if err := e.fakeEngineSession.CloseFor(ctx, sid, mode, reason); err != nil {
		return err
	}
	if mode == "abort" && e.ends != nil {
		e.once.Do(func() { close(e.ends) })
	}
	return nil
}

func TestTheSwitchEndsThePredecessorsSessionsFirst(t *testing.T) {
	dir := t.TempDir()
	pkg := buildResponderPkg(t, dir, "org.example.speaks")
	reg := tools.NewRegistry(dir, nil, tools.Timeouts{})
	a := &App{pluginToolReg: reg, pluginOpts: &pluginhost.Options{}}
	stand := func(shadow bool) *pluginhost.Activation {
		t.Helper()
		activate := pluginhost.Activate
		if shadow {
			activate = pluginhost.ActivateShadow
		}
		ap, err := activate(t.Context(), pkg, reg, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = ap.Deactivate(context.Background()) })
		ap.Voice.Store(newEngineWire(t).v)
		return &pluginhost.Activation{ID: ap.ID, Version: ap.Version, Package: pkg, Plugin: ap}
	}
	const name = "pl_org_example_speaks_ping"
	owner := func() any {
		got, ok := reg.Get(name)
		if !ok {
			return nil
		}
		return got.(interface{ DispatchOwner() any }).DispatchOwner()
	}
	// session opens one on the activation named; closing what it answers
	// is the engine's word that the session is over.
	session := func(id string, on *pluginhost.Activation, v engineSession) chan struct{} {
		over := make(chan struct{})
		a.voiceSessions.Store(id, &voiceHandle{id: id, v: v, b: &audio.Binding{InputHandle: "in-" + id, Contained: true}, done: over, drained: make(chan struct{}), engine: on.Plugin})
		return over
	}
	held := func() bool {
		ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
		defer cancel()
		release, err := a.voiceOpens.take(ctx)
		if err == nil {
			release()
		}
		return err != nil
	}
	old := stand(false)
	a.adoptPlugin(old, nil)

	// The session ends by itself within the bound: the switch waits for it,
	// holding every new open meanwhile.
	next := stand(true)
	open := session("vs-talking", old, &fakeEngineSession{})
	rt := newPluginRuntime(a)
	switched := make(chan error, 1)
	go func() { switched <- rt.Redirect(old, next) }()
	// The switch has begun when it holds new sessions, on its own
	// goroutine: that is waited for, where 200 ms used to stand for it. A
	// switch that returned instead is told apart below.
	holdWait(t, "the switch to hold new sessions, or to return", func() bool { return held() || len(switched) > 0 })
	select {
	case err := <-switched:
		t.Fatalf("the successor took the plugin while the predecessor's session was open (%v)", err)
	default:
	}
	if owner() != old.Plugin || !held() {
		t.Fatalf("while the predecessor's session is open: the name is %v's, new sessions held=%v", owner(), held())
	}
	close(open) // the operator ends the conversation
	select {
	case err := <-switched:
		if err != nil {
			t.Fatalf("the switch after the session ended: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the switch never followed the session's end")
	}
	if owner() != next.Plugin || held() {
		t.Fatalf("after the switch: the name is %v's, new sessions held=%v", owner(), held())
	}

	// The session outlasts the bound: it is ended as the operator's stop
	// ends one, and the switch follows the engine's word that it is over.
	last := stand(true)
	driver := &endsWhenAborted{fakeEngineSession: &fakeEngineSession{}, ends: make(chan struct{})}
	open = session("vs-earbuds", next, driver)
	go func() { <-driver.ends; close(open) }()
	rt.handOver = 200 * time.Millisecond
	if err := rt.Redirect(next, last); err != nil {
		t.Fatalf("the switch past the bound: %v", err)
	}
	driver.mu.Lock()
	closed := strings.Join(driver.closed, "; ")
	driver.mu.Unlock()
	if closed != "abort: operator" || owner() != last.Plugin {
		t.Fatalf("past the bound: the session was closed %q; the name is %v's", closed, owner())
	}

	// A session that will not end even when asked: the predecessor keeps
	// serving, the switch is refused, and new sessions are no longer held.
	stuck := session("vs-stuck", last, &endsWhenAborted{fakeEngineSession: &fakeEngineSession{}})
	defer close(stuck)
	err := rt.Redirect(last, stand(true))
	if !errors.Is(err, pluginhost.ErrHandOverBusy) || owner() != last.Plugin || held() {
		t.Fatalf("a session that will not end: %v; the name is %v's; new sessions held=%v", err, owner(), held())
	}
}
