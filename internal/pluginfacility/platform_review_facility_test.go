// .
// .
// .
// .
// .
// .

package pluginfacility

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestReviewBudgetCannotOverrideKnownMemoryExhaustion(t *testing.T) {
	f := New(Config{Capacity: fixedCapacity{total: 8 * gib, avail: gib}})
	f.policy = Policy{Admission: AdmissionPolicy{ReserveBytes: 2 * gib, BudgetBytes: 4 * gib}}
	ok, why, err := f.admissibleLocked(&admitWaiter{}, Prepared{HostBytes: gib})
	if ok {
		t.Fatalf("admitted despite measured free below reserve: %s (%v)", why, err)
	}
}

type reviewCapacity struct{ available atomic.Int64 }

func (c *reviewCapacity) Measure() Availability {
	return Availability{HostKnown: true, HostTotal: 8 * gib, HostAvailable: c.available.Load()}
}

func TestReviewAdmissionRechecksCapacityAfterObservation(t *testing.T) {
	rt := newSharedRuntime()
	rt.hostBytesFor["one.aiiospkg"] = 2 * gib
	cap := &reviewCapacity{}
	cap.available.Store(gib)
	f := New(Config{Runtime: rt, Capacity: cap})
	auditAttachFacility(t, f)
	f.Observe(obs("one.aiiospkg"), Policy{Revision: 1})
	auditWaitFacility(t, "memory waiter", func() bool { return viewOf(f, idOf("one.aiiospkg")).State == StateAdmitting })
	// .
	auditWaitPass(t, f)
	cap.available.Store(4 * gib)
	f.Observe(obs("one.aiiospkg"), Policy{Revision: 2})
	auditWaitPass(t, f)
	deadline := time.Now().Add(300 * time.Millisecond)
	for viewOf(f, idOf("one.aiiospkg")).State != StateActive && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	stalled := viewOf(f, idOf("one.aiiospkg")).State != StateActive
	// .
	f.mu.Lock()
	f.ringDoorbellsLocked()
	f.mu.Unlock()
	auditWaitFacility(t, "admission after explicit wake", func() bool { return viewOf(f, idOf("one.aiiospkg")).State == StateActive })
	if stalled {
		t.Fatal("fresh capacity and observed policy did not wake admission; explicit doorbell did")
	}
}

type reviewHeldStart struct {
	*sharedRuntime
	first   atomic.Bool
	ctxSeen chan context.Context
	release chan struct{}
}

func (r *reviewHeldStart) Start(ctx context.Context, p Prepared, l *Lease) (Running, error) {
	if r.first.CompareAndSwap(false, true) {
		r.ctxSeen <- ctx
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-r.release:
		}
	}
	return r.sharedRuntime.Start(ctx, p, l)
}
func TestReviewNewIntentCancelsThroughController(t *testing.T) {
	rt := &reviewHeldStart{sharedRuntime: newSharedRuntime(), ctxSeen: make(chan context.Context, 1), release: make(chan struct{})}
	f := auditFacility(t, rt)
	t.Cleanup(func() { close(rt.release) })
	f.Observe(obs("one.aiiospkg"), Policy{Revision: 1})
	var old context.Context
	select {
	case old = <-rt.ctxSeen:
	case <-time.After(time.Second):
		t.Fatal("start not reached")
	}
	set := obs("one.aiiospkg")
	set[0].Hash = "sha256:replacement"
	f.Observe(set, Policy{Revision: 2})
	auditWaitPass(t, f)
	select {
	case <-old.Done():
	case <-time.After(300 * time.Millisecond):
		t.Fatal("controller did not cancel obsolete in-flight start after replacement intent")
	}
}

func TestReviewRetirementRetryKeepsDeadlineAndControllerResponsive(t *testing.T) {
	f := New(Config{})
	l := NewLease(1)
	calls := 0
	entered := make(chan bool, 1)
	release := make(chan struct{})
	done := make(chan struct{})
	l.Hold("child", func() error {
		calls++
		if calls == 1 {
			return errors.New("pending")
		}
		_, bounded := l.Context().Deadline()
		entered <- bounded
		<-release
		return errors.New("still pending")
	})
	if l.Release() == nil {
		t.Fatal("setup should retain child")
	}
	f.instances["id.example.one"] = &Instance{ID: "id.example.one", Activations: []*Activation{{Gen: 1, Role: RoleRetiring, Lease: l}}}
	go func() { defer close(done); f.pass() }()
	var bounded bool
	select {
	case bounded = <-entered:
	case <-time.After(time.Second):
		t.Fatal("retry not reached")
	}
	blocked := false
	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
		blocked = true
	}
	close(release)
	<-done
	if !bounded || blocked {
		t.Fatalf("retirement retry runs inside controller: deadline=%v controller_blocked=%v", bounded, blocked)
	}
}

func TestReviewReleasePanicDoesNotReplaySuccessfulCleanup(t *testing.T) {
	l := NewLease(1)
	newestCalls := 0
	panicCalls := 0
	l.Hold("root", func() error { return nil })
	l.Hold("middle", func() error {
		panicCalls++
		if panicCalls == 1 {
			panic("injected cleanup panic")
		}
		return nil
	})
	l.Hold("newest", func() error { newestCalls++; return nil })
	e := &executor{}
	if e.releaseGuarded(context.Background(), l) == nil {
		t.Fatal("panic not reported")
	}
	if err := e.releaseGuarded(context.Background(), l); err != nil {
		t.Fatal(err)
	}
	if newestCalls != 1 {
		t.Fatalf("successful cleanup replayed after sibling panic: newest called %d times", newestCalls)
	}
}

func TestReviewDiscoveryRefusalsReachSnapshot(t *testing.T) {
	rt := newSharedRuntime()
	rt.verifyErrFor["broken.aiiospkg"] = errors.New("signature does not verify")
	f := New(Config{Runtime: rt, Discover: func() Discovery {
		return Discovery{Found: []Found{{Dir: "plugins/broken", Package: "broken.aiiospkg", Size: 1, MTime: 1}}, Ambiguous: []string{"plugins/ambiguous"}}
	}})
	f.Rescan(Policy{Revision: 1})
	snap := f.Snapshot()
	if len(snap.Instances) == 0 && len(snap.Skips) == 0 {
		t.Fatal("broken and ambiguous installed packages disappeared from facility snapshot; only logs retain refusal")
	}
}
