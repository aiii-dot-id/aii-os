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

func TestReviewAdmissibleToolBypassesUnfitVoice(t *testing.T) {
	f := New(Config{Capacity: fixedCapacity{total: 8 * gib, avail: 4 * gib}})
	voice := &admitWaiter{seq: 1, gen: 1, id: "voice", voice: true}
	tool := &admitWaiter{seq: 2, gen: 2, id: "tool"}
	f.queue = []*admitWaiter{voice, tool}
	f.mu.Lock()
	defer f.mu.Unlock()
	ok, why, never := f.admissibleLocked(voice, Prepared{HostBytes: 6 * gib})
	if ok || never != nil {
		t.Fatalf("fixture: %v %s %v", ok, why, never)
	}
	ok, why, never = f.admissibleLocked(tool, Prepared{HostBytes: gib})
	if !ok {
		t.Fatalf("admissible tool blocked: %s (%v)", why, never)
	}
}

type reviewReadmitRuntime struct {
	*fakeRuntime
	calls           atomic.Int32
	entered, resume chan struct{}
}

func (r *reviewReadmitRuntime) Verify(ctx context.Context, pkg string) (Evidence, error) {
	if r.calls.Add(1) == 2 {
		close(r.entered)
		<-r.resume
	}
	return r.fakeRuntime.Verify(ctx, pkg)
}
func TestReviewSupersededPendingUpdateLeavesNoOrphan(t *testing.T) {
	r := &reviewReadmitRuntime{fakeRuntime: newFakeRuntime(), entered: make(chan struct{}), resume: make(chan struct{})}
	f := newFacility(t, r)
	const id = "id.example.p"
	one := []Observed{{ID: id, Package: "one", Hash: "one"}}
	two := []Observed{{ID: id, Package: "two", Hash: "two"}}
	f.Observe(one, Policy{Revision: 1})
	until(t, "one active", func() bool { return viewOf(f, id).State == StateActive })
	f.Observe(one, Policy{Revision: 2})
	auditExecutorWait(t, r.entered, "readmission held")
	released := false
	defer func() {
		if !released {
			close(r.resume)
		}
	}()
	f.Observe(two, Policy{Revision: 2})
	pendingKind := func(kind commandKind) bool {
		f.mu.Lock()
		e := f.execs[id]
		f.mu.Unlock()
		e.mu.Lock()
		defer e.mu.Unlock()
		return e.pending != nil && e.pending.kind == kind
	}
	until(t, "update queued", func() bool { return pendingKind(cmdUpdate) })
	f.Observe(nil, Policy{Revision: 2})
	until(t, "deactivate supersedes queued update", func() bool { return pendingKind(cmdDeactivate) })
	close(r.resume)
	released = true
	until(t, "predecessor retirement", func() bool { return sawCall(r.fakeRuntime, "stop:one") })
	f.Observe(two, Policy{Revision: 2})
	time.Sleep(50 * time.Millisecond)
	f.mu.Lock()
	defer f.mu.Unlock()
	inst := f.instances[id]
	if inst != nil && inst.Candidate() != nil {
		t.Fatalf("superseded generation %d remains candidate; plan cannot restart", inst.Candidate().Gen)
	}
}

func TestReviewControllerDoesNotRunUnboundedResidualCleanup(t *testing.T) {
	f := New(Config{})
	lease := NewLease(1)
	var calls atomic.Int32
	entered, release := make(chan struct{}), make(chan struct{})
	lease.Hold("activation", func() error {
		if calls.Add(1) == 1 {
			return errors.New("still owed")
		}
		close(entered)
		<-release
		return nil
	})
	_ = lease.Release()
	a := NewActivation(1, "old", "1", "p", "h", time.Now())
	a.Lease = lease
	a.Role = RoleRetiring
	f.instances["old"] = &Instance{ID: "old", Activations: []*Activation{a}}
	done := make(chan struct{})
	go func() { f.pass(); close(done) }()
	auditExecutorWait(t, entered, "residual cleanup")
	select {
	case <-done:
		close(release)
	case <-time.After(100 * time.Millisecond):
		close(release)
		auditExecutorWait(t, done, "released controller")
		t.Fatal("controller pass blocked in residue callback with Background context")
	}
}
