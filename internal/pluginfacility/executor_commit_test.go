package pluginfacility

import (
	"context"
	"sync"
	"testing"
)

func TestAnAttemptThatServesIsNotTakenBackByTheNextCommand(t *testing.T) {
	rt := newFakeRuntime()
	col := &collector{}
	ctx, cancel := context.WithCancel(context.Background())
	var gen Generation
	var mu sync.Mutex
	var e *executor
	var served *attempt
	e = newExecutor("id.example.p", executorDeps{
		Runtime: rt,
		NextGen: func() Generation { mu.Lock(); defer mu.Unlock(); gen++; return gen },
		Emit: func(ev Event) {
			if ev.Kind == EventActive && served == nil {
				served = e.running // published under the executor's lock
			}
			col.emit(ev)
		},
	}, ctx)
	t.Cleanup(func() { cancel(); e.Close(); e.Wait() })
	c := newCommand(cmdActivate, "p1", "h")
	e.Send(c)
	waitDone(t, c, "the activation")
	if served == nil || served.lease == nil || !served.lease.Authorized() {
		t.Fatalf("the activation as it was published: %+v", served)
	}
	// run lets go of the attempt after the command settles; put it back only
	// once it has, or the executor's own letting go would empty the window
	// and the check below would prove nothing.
	waitFor(t, "the executor to let go of the activation", func() bool {
		e.mu.Lock()
		defer e.mu.Unlock()
		return e.running == nil
	})
	e.mu.Lock()
	e.running = served // published active, not yet let go of
	e.mu.Unlock()
	rt.startGate = make(chan struct{}) // the next command waits at its start
	next := newCommand(cmdUpdate, "p2", "h2")
	e.Send(next)
	if !served.lease.Authorized() {
		t.Fatal("the command sent after the activation was published withdrew the serving activation's authorization")
	}
	close(rt.startGate)
	waitDone(t, next, "the next command")
	// Then the update serves and retires what it replaced, in the ordinary way.
	if last := col.last(EventActive); last == nil || last.Gen == served.gen || served.lease.Authorized() {
		t.Fatalf("the update should serve and retire the activation it replaced: %v", col.kinds())
	}
}
