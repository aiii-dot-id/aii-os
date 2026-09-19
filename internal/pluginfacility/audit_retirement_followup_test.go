// .
// .
// .
// .
// .

package pluginfacility

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// .
func TestAuditCleanupPanicCommitsCompletedReleases(t *testing.T) {
	l := NewLease(801)
	panicNow := true
	top, bottom := 0, 0
	l.Hold("root", func() error { bottom++; return nil })
	l.Hold("child", func() error {
		if panicNow {
			panic("stop panicked")
		}
		return nil
	})
	l.Hold("route", func() error { top++; return nil })
	e := &executor{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := e.releaseGuarded(ctx, l); err == nil {
		t.Fatal("panic lost")
	}
	if len(l.Residue()) == 0 {
		t.Error("panicking child has no named residue")
	}
	if got := strings.Join(l.Holds(), ","); got != "root,child" {
		t.Errorf("completed release retained: %s", got)
	}
	if l.Context() == ctx {
		t.Error("completed release retained its context")
	}
	if bottom != 0 {
		t.Error("dependency released through panic")
	}
	panicNow = false
	if err := l.Release(); err != nil {
		t.Fatal(err)
	}
	if top != 1 || bottom != 1 {
		t.Errorf("cleanup was not exactly once: route=%d root=%d", top, bottom)
	}
}

func TestAuditCleanupPanicCannotEscapeControllerRetry(t *testing.T) {
	f := New(Config{Runtime: newSharedRuntime()})
	i := newInst()
	i.Desired = Desired{}
	a := i.add(802, RoleRetiring)
	panicNow := true
	a.Lease.Hold("child", func() error {
		if panicNow {
			panic("stop panicked")
		}
		return nil
	})
	_ = (&executor{}).releaseGuarded(context.Background(), a.Lease)
	f.instances[i.ID] = i
	var escaped any
	func() { defer func() { escaped = recover() }(); f.pass() }()
	panicNow = false
	_ = a.Lease.Release()
	if escaped != nil {
		t.Errorf("cleanup panic escaped controller pass: %v", escaped)
	}
}

func TestAuditLateHandoffAfterPruneKeepsAnOwner(t *testing.T) {
	i := newInst()
	i.add(1, RoleActive)
	a := i.add(2, RoleRetiring)
	if err := a.Lease.Release(); err != nil {
		t.Fatal(err)
	}
	i.Prune()
	returned := false
	a.Lease.Hold("late child", func() error { returned = true; return nil })
	owned := false
	for _, known := range i.Activations {
		if known == a {
			owned = true
		}
	}
	if !owned && !returned {
		t.Error("late child has no remaining instance owner or cleanup")
	}
	// .
	_ = a.Lease.Release()
}

func TestAuditFailedCandidateCustodyBlocksDirectExecutor(t *testing.T) {
	rt := &auditExecutorRuntime{fakeRuntime: newFakeRuntime()}
	rt.healthErr = errors.New("candidate unhealthy")
	var reaped atomic.Bool
	rt.stopHook = func(context.Context, Running) (Retirement, error) {
		return Retirement{Established: reaped.Load(), Residue: []string{"child remains"}}, nil
	}
	e, _ := newTestExecutor(t, rt, &collector{})
	var commands []*command
	for _, pkg := range []string{"one", "two", "three"} {
		commands = append(commands, auditExecutorCommand(t, e, cmdActivate, pkg))
	}
	e.Close()
	e.Wait()
	starts := 0
	for _, call := range rt.seen() {
		if strings.HasPrefix(call, "start:") {
			starts++
		}
	}
	reaped.Store(true)
	for _, c := range commands {
		_ = c.lease.Release()
	}
	if starts != 1 {
		t.Errorf("started %d candidates while the first child remained held", starts)
	}
}

func TestAuditPolicyWithdrawalDuringHealthPreventsAdmission(t *testing.T) {
	rt := &auditExecutorRuntime{fakeRuntime: newFakeRuntime()}
	entered := make(chan struct{})
	gate, release := auditExecutorBarrier()
	var withdrew, lateAdmission atomic.Bool
	rt.healthHook = func(context.Context, Running) error { close(entered); <-gate; return nil }
	rt.redirectHook = func(Running, Running) error {
		if withdrew.Load() {
			lateAdmission.Store(true)
		}
		return nil
	}
	f := auditFacility(t, rt)
	defer release()
	set := []Observed{{ID: "id.example.p", Package: "one", Hash: "sha256:aa"}}
	f.Observe(set, Policy{Revision: 1})
	auditExecutorWait(t, entered, "held Health")
	f.Observe(set, Policy{Revision: 2, Safe: true})
	auditWaitPass(t, f)
	withdrew.Store(true)
	release()
	auditWaitFacility(t, "withdrawal completion", func() bool { return strings.Contains(strings.Join(rt.seen(), ","), "stop:one") })
	if lateAdmission.Load() {
		t.Error("Redirect admitted startup after SAFE was observed and reconciled")
	}
}

func TestAuditControllerRetryDoesNotBlockLaterPlugin(t *testing.T) {
	rt := newSharedRuntime()
	f := New(Config{Runtime: rt})
	i := newInst()
	i.Desired = Desired{}
	a := i.add(804, RoleRetiring)
	entered := make(chan struct{})
	gate, release := auditExecutorBarrier()
	var calls atomic.Int32
	a.Lease.Hold("child", func() error {
		if calls.Add(1) == 1 {
			return errors.New("not reaped")
		}
		close(entered)
		<-gate
		return nil
	})
	_ = a.Lease.Release()
	f.instances[i.ID] = i
	auditAttachFacility(t, f)
	defer release()
	auditExecutorWait(t, entered, "controller retry")
	f.Observe(obs("two.aiiospkg"), Policy{Revision: 1})
	deadline := time.Now().Add(150 * time.Millisecond)
	for !strings.Contains(rt.seen(), "start:two.aiiospkg") && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	startedBeforeRelease := strings.Contains(rt.seen(), "start:two.aiiospkg")
	release()
	auditWaitFacility(t, "unrelated plugin starts once retry releases", func() bool { return strings.Contains(rt.seen(), "start:two.aiiospkg") })
	if !startedBeforeRelease {
		t.Error("one pending retirement blocked a later unrelated plugin until its cleanup released")
	}
}

func TestAuditShutdownRevisitsPendingPredecessor(t *testing.T) {
	rt := &auditExecutorRuntime{fakeRuntime: newFakeRuntime()}
	var oldCalls atomic.Int32
	var ready atomic.Bool
	rt.stopHook = func(_ context.Context, r Running) (Retirement, error) {
		if r.(*fakeRunning).tag == "one" {
			oldCalls.Add(1)
			return Retirement{Established: ready.Load(), Residue: []string{"child remains"}}, nil
		}
		return Retirement{Established: true}, nil
	}
	col := &collector{}
	e, _ := newTestExecutor(t, rt, col)
	first := auditExecutorCommand(t, e, cmdActivate, "one")
	auditExecutorCommand(t, e, cmdUpdate, "two")
	waitFor(t, "pending retirement", func() bool { return col.last(EventRetired) != nil })
	ready.Store(true)
	e.Close()
	e.Wait()
	before := oldCalls.Load()
	pending := !first.lease.Discharged()
	_ = first.lease.Release()
	if before != 2 || pending {
		t.Errorf("shutdown forgot pending predecessor: Stop calls=%d pending=%v", before, pending)
	}
}
