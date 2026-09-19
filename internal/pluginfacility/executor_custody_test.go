package pluginfacility

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

// .
// .
// .

// .
func (c *collector) all() []Event {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]Event(nil), c.ev...)
}

func sawCall(rt *fakeRuntime, call string) bool {
	for _, c := range rt.seen() {
		if c == call {
			return true
		}
	}
	return false
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("never: %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// .
func TestAPanicAfterAHoldGivesTheHoldBack(t *testing.T) {
	rt := newFakeRuntime()
	rt.healthPanics = true
	col := &collector{}
	e, cancel := newTestExecutor(t, rt, col)
	defer cancel()
	c := newCommand(cmdActivate, "p1", "h")
	e.Send(c)
	waitDone(t, c, "the panicking activation")
	ref := col.last(EventRefused)
	if ref == nil || ref.Refusal == nil || ref.Refusal.Stage != StagePanic {
		t.Fatalf("a panic is a refusal at the panic stage: %+v", ref)
	}
	if !sawCall(rt, "stop:p1") || !sawCall(rt, "release:binding") {
		t.Fatalf("the child and the binding the attempt held come back through its ledger: %v", rt.seen())
	}
	if !c.lease.Discharged() {
		t.Fatalf("the finalizer discharges the attempt's custody: still %v", c.lease.Holds())
	}
	if ref.Refusal.Cleanup != nil {
		t.Fatalf("a clean release carries no cleanup error: %v", ref.Refusal.Cleanup)
	}
}

// .
// .
func TestTheHostStoppingRetiresTheActiveChild(t *testing.T) {
	rt := newFakeRuntime()
	col := &collector{}
	e, cancel := newTestExecutor(t, rt, col)
	defer cancel()
	c := newCommand(cmdActivate, "p1", "h")
	e.Send(c)
	waitDone(t, c, "the activation")
	if col.last(EventActive) == nil {
		t.Fatal("it serves")
	}
	e.Close()
	e.Wait()
	if !sawCall(rt, "stop:p1") {
		t.Fatalf("the executor left with a child still serving: %v", rt.seen())
	}
	ret := col.last(EventRetired)
	if ret == nil || ret.Retirement == nil || !ret.Retirement.Established {
		t.Fatalf("retirement is published, established, before Wait returns: %+v", ret)
	}
}

// .
// .
// .
func TestAHeldPredecessorStopDoesNotBlockTheSuccessor(t *testing.T) {
	rt := newFakeRuntime()
	rt.stopGate = map[string]chan struct{}{"p1": make(chan struct{})}
	col := &collector{}
	e, cancel := newTestExecutor(t, rt, col)
	defer cancel()
	first := newCommand(cmdActivate, "p1", "h1")
	e.Send(first)
	waitDone(t, first, "the first activation")
	gen1 := col.last(EventActive).Gen
	second := newCommand(cmdActivate, "p2", "h2")
	e.Send(second)
	waitDone(t, second, "the replacement")
	waitFor(t, "the predecessor's stop to be asked for", func() bool { return sawCall(rt, "stop:p1") })
	// .
	stop := newCommand(cmdDeactivate, "", "")
	e.Send(stop)
	waitDone(t, stop, "deactivating the successor")
	waitFor(t, "the successor to be stopped while the predecessor is still going", func() bool { return sawCall(rt, "stop:p2") })
	for _, ev := range col.all() {
		if ev.Kind == EventRetired && ev.Gen == gen1 {
			t.Fatal("the predecessor was reported retired while its Stop was still held")
		}
	}
	close(rt.stopGate["p1"])
	waitFor(t, "the predecessor's retirement to arrive under its own generation", func() bool {
		for _, ev := range col.all() {
			if ev.Kind == EventRetired && ev.Gen == gen1 && ev.Retirement != nil && ev.Retirement.Established {
				return true
			}
		}
		return false
	})
}

// .
// .
func TestARetirementThatNeverEstablishesIsPublishedPendingUnderItsBound(t *testing.T) {
	rt := newFakeRuntime()
	rt.stopGate = map[string]chan struct{}{"p1": make(chan struct{})}
	col := &collector{}
	e, cancel := newTestExecutorWith(t, rt, col, executorDeps{RetireTimeout: 50 * time.Millisecond})
	defer cancel()
	defer close(rt.stopGate["p1"])
	c := newCommand(cmdActivate, "p1", "h")
	e.Send(c)
	waitDone(t, c, "the activation")
	stop := newCommand(cmdDeactivate, "", "")
	e.Send(stop)
	waitDone(t, stop, "the deactivate")
	waitFor(t, "a pending retirement under the bound", func() bool {
		ret := col.last(EventRetired)
		return ret != nil && ret.Retirement != nil && !ret.Retirement.Established && len(ret.Retirement.Residue) > 0
	})
	if ret := col.last(EventRetired); !strings.Contains(strings.Join(ret.Retirement.Residue, " "), "still stopping") {
		t.Fatalf("the residue says what is still held and why: %v", ret.Retirement.Residue)
	}
}

// .
// .
// .
func TestAWithdrawalDuringTheRedirectNeverPublishesActive(t *testing.T) {
	rt := newFakeRuntime()
	rt.redirectGate = make(chan struct{})
	col := &collector{}
	e, cancel := newTestExecutor(t, rt, col)
	defer cancel()
	c := newCommand(cmdActivate, "p1", "h")
	e.Send(c)
	waitFor(t, "the redirect to be in flight", func() bool { return sawCall(rt, "admit") })
	e.Close()
	close(rt.redirectGate)
	e.Wait()
	waitDone(t, c, "the withdrawn activation")
	if col.last(EventActive) != nil {
		t.Fatal("an activation withdrawn during its redirect was published as serving")
	}
	if !sawCall(rt, "stop:p1") {
		t.Fatalf("the child the redirect made reachable must be stopped: %v", rt.seen())
	}
	ref := col.last(EventRefused)
	if ref == nil || ref.Refusal == nil || ref.Refusal.Stage != StageCancelled {
		t.Fatalf("it is refused as cancelled: %+v", ref)
	}
}

func newTestExecutorWith(t *testing.T, rt Runtime, col *collector, extra executorDeps) (*executor, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	var gen Generation
	var mu sync.Mutex
	deps := executorDeps{Runtime: rt, Emit: col.emit, RetireTimeout: extra.RetireTimeout,
		NextGen: func() Generation { mu.Lock(); defer mu.Unlock(); gen++; return gen }}
	e := newExecutor("id.example.p", deps, ctx)
	t.Cleanup(func() { cancel(); e.Close(); e.Wait() })
	return e, cancel
}
