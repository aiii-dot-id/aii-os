// .
// .
// .
// .
// .
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
	"strings"
	"sync"
	"testing"
	"time"
)

// .
// .
func auditFacility(t *testing.T, rt Runtime) *Facility {
	t.Helper()
	f := New(Config{Runtime: rt, Capacity: ampleCapacity{}})
	auditAttachFacility(t, f)
	return f
}

func auditAttachFacility(t *testing.T, f *Facility) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	var workers sync.WaitGroup
	f.cfg.Spawn = func(fn func()) bool {
		if ctx.Err() != nil {
			return false
		}
		workers.Add(1)
		go func() { defer workers.Done(); fn() }()
		return true
	}
	f.Attach(ctx)
	t.Cleanup(func() { cancel(); f.Close(); workers.Wait() })
}

func auditWaitFacility(t *testing.T, what string, ready func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !ready() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

func auditWaitPass(t *testing.T, f *Facility) {
	t.Helper()
	rev := f.Snapshot().Revision
	f.Poke("audit completion barrier")
	auditWaitFacility(t, "a reconciliation pass", func() bool { return f.Snapshot().Revision > rev })
}

func TestAuditFacilityRechecksPolicyForActiveBytes(t *testing.T) {
	for _, change := range []string{"safe", "autoload-denied", "trust-changed"} {
		t.Run(change, func(t *testing.T) {
			rt := newSharedRuntime()
			f := auditFacility(t, rt)
			f.Observe(obs("one.aiiospkg"), Policy{Revision: 1, TrustGen: 1})
			auditWaitFacility(t, "active plugin", func() bool { return f.stateOf("id.example.one") == StateActive })
			next := Policy{Revision: 2, TrustGen: 1}
			switch change {
			case "safe":
				next.Safe = true
			case "autoload-denied":
				next.Allows = func(Evidence) (bool, string) { return false, "autoload disabled" }
			case "trust-changed":
				next.TrustGen = 2
				rt.mu.Lock()
				rt.verifyErrFor["one.aiiospkg"] = errors.New("signer has been revoked")
				rt.mu.Unlock()
			}
			f.Observe(obs("one.aiiospkg"), next)
			auditWaitPass(t, f)
			// .
			deadline := time.Now().Add(250 * time.Millisecond)
			for f.stateOf("id.example.one") == StateActive && time.Now().Before(deadline) {
				time.Sleep(time.Millisecond)
			}
			// .
			// .
			// .
			stopBy := time.Now().Add(2 * time.Second)
			for !strings.Contains(rt.seen(), "stop:one.aiiospkg") && time.Now().Before(stopBy) {
				time.Sleep(time.Millisecond)
			}
			if f.stateOf("id.example.one") == StateActive || !strings.Contains(rt.seen(), "stop:one.aiiospkg") {
				t.Errorf("%s left old bytes active without withdrawal: %s", change, rt.seen())
			}
		})
	}
}

func TestAuditFacilityPermanentRefusalWakesOnItsInput(t *testing.T) {
	for _, input := range []string{"policy", "package", "trust"} {
		t.Run(input, func(t *testing.T) {
			rt := newSharedRuntime()
			f := auditFacility(t, rt)
			pol := Policy{Revision: 1, TrustGen: 1}
			if input == "policy" {
				pol.Safe = true
			} else {
				rt.verifyErrFor["one.aiiospkg"] = errors.New("old package refused")
			}
			f.Observe(obs("one.aiiospkg"), pol)
			auditWaitFacility(t, "permanent refusal", func() bool {
				s := f.stateOf("id.example.one")
				return s == StateSkipped || s == StateRefused
			})
			rt.mu.Lock()
			delete(rt.verifyErrFor, "one.aiiospkg")
			rt.mu.Unlock()
			set := obs("one.aiiospkg")
			switch input {
			case "policy":
				pol.Safe, pol.Revision = false, 2
			case "trust":
				pol.TrustGen = 2
			case "package":
				set[0].Hash = "sha256:replacement"
			}
			f.Observe(set, pol)
			auditWaitPass(t, f)
			deadline := time.Now().Add(250 * time.Millisecond)
			for f.stateOf("id.example.one") != StateActive && time.Now().Before(deadline) {
				time.Sleep(time.Millisecond)
			}
			if f.stateOf("id.example.one") != StateActive {
				t.Errorf("changed %s did not reopen its permanent refusal: state=%s calls=%s", input, f.stateOf("id.example.one"), rt.seen())
			}
		})
	}
}

func TestAuditFacilityTransientRetryHasBackoff(t *testing.T) {
	rt := newSharedRuntime()
	rt.startErrFor["one.aiiospkg"] = errors.New("temporary start failure")
	f := auditFacility(t, rt)
	f.Observe(obs("one.aiiospkg"), Policy{Revision: 1})
	auditWaitFacility(t, "first attempt", func() bool { return strings.Count(rt.seen(), "start:") > 0 })
	time.Sleep(100 * time.Millisecond)
	if n := strings.Count(rt.seen(), "start:"); n != 1 {
		t.Errorf("transient failure consumed %d attempts before any backoff could elapse: %s", n, rt.seen())
	}
}

func TestAuditFacilityBudgetExpiryWakesWithoutAnotherEvent(t *testing.T) {
	rt := newSharedRuntime()
	f := New(Config{Runtime: rt})
	f.Observe(obs("one.aiiospkg"), Policy{Revision: 1})
	i := f.instances["id.example.one"]
	i.Budget = Budget{Max: 1, Window: 100 * time.Millisecond}
	i.Budget.For(intentOf(i.ID, "", i.PackageHash, Policy{Revision: 1}))
	i.Budget.Record(time.Now())
	auditAttachFacility(t, f)
	auditWaitPass(t, f)
	time.Sleep(300 * time.Millisecond)
	if !strings.Contains(rt.seen(), "start:") {
		t.Error("budget reopened, but no timer woke the controller to retry")
	}
}

func TestAuditFacilityClosePreventsSubsequentStarts(t *testing.T) {
	rt := newSharedRuntime()
	f := auditFacility(t, rt)
	f.Close()
	f.Observe(obs("two.aiiospkg"), Policy{Revision: 1})
	time.Sleep(100 * time.Millisecond)
	if got := rt.seen(); strings.Contains(got, "start:") {
		t.Errorf("Facility.Close returned but its controller admitted a new plugin: %s", got)
	}
}

func TestAuditFacilityKnownStaleGenerationCannotBecomeActive(t *testing.T) {
	f := New(Config{Runtime: newSharedRuntime()})
	old := NewActivation(1, "id.example.one", "1", "old", "hash-old", time.Now())
	old.Role = RoleRetiring
	current := NewActivation(2, old.PluginID, "2", "new", "hash-new", time.Now())
	current.Role = RoleActive
	f.instances[old.PluginID] = &Instance{ID: old.PluginID, Package: "new", PackageHash: "hash-new",
		Desired: Desired{Active: true, Package: "new", Hash: "hash-new"}, Activations: []*Activation{old, current}}
	f.record(Event{PluginID: old.PluginID, Gen: old.Gen, Kind: EventActive, Version: "1"})
	if old.Role == RoleActive || current.Role != RoleActive {
		t.Errorf("known stale report promoted old generation and displaced successor: old=%s current=%s", old.Role, current.Role)
	}
}

func TestAuditFacilitySnapshotDoesNotExposeMutableState(t *testing.T) {
	f := New(Config{Runtime: newSharedRuntime()})
	a := NewActivation(1, "id.example.one", "1", "one", "hash", time.Now())
	a.Role = RoleRefused
	a.Refusal = NewRefusal(a.PluginID, "1", a.Gen, StageVerify, errors.New("rejected"))
	a.Material = &MaterialStatus{BytesPresent: 1, BytesTotal: 9}
	f.instances[a.PluginID] = &Instance{ID: a.PluginID, Activations: []*Activation{a}}
	view := f.Snapshot().Instances[0]
	view.Refusal.Class = ClassTransient
	view.Refusal.WakeOn[0] = InputConfig
	view.Material.BytesPresent = 99
	if a.Refusal.Class != ClassPermanent || a.Refusal.WakeOn[0] != InputPackage || a.Material.BytesPresent != 1 {
		t.Errorf("mutating a returned snapshot changed controller state: class=%s wake=%v material=%d", a.Refusal.Class, a.Refusal.WakeOn, a.Material.BytesPresent)
	}
}

func TestAuditFacilityPendingFailedStartBlocksAnotherChild(t *testing.T) {
	rt := newFakeRuntime()
	rt.healthErr = errors.New("candidate failed Health")
	rt.stopEstablished = false
	rt.stopResidue = []string{"child still running"}
	f := auditFacility(t, rt)
	f.Observe([]Observed{{ID: "id.example.p", Package: "one", Hash: "sha256:aa"}}, Policy{Revision: 1})
	auditWaitFacility(t, "first failed child", func() bool {
		return strings.Contains(strings.Join(rt.seen(), ","), "stop:one")
	})
	time.Sleep(100 * time.Millisecond)
	if got := strings.Join(rt.seen(), ","); strings.Count(got, "start:one") != 1 {
		t.Errorf("unretired failed candidate did not block another child: %s", got)
	}
}

func TestAuditFacilitySnapshotDuringCleanupBlocksOtherObserve(t *testing.T) {
	f := New(Config{Runtime: newSharedRuntime()})
	a := NewActivation(1, "id.example.one", "1", "one", "hash", time.Now())
	a.Role = RoleRetiring
	gate, release := auditExecutorBarrier()
	defer release()
	entered, released := make(chan struct{}), make(chan struct{})
	a.Lease.Hold("child", func() error { close(entered); <-gate; return nil })
	f.instances[a.PluginID] = &Instance{ID: a.PluginID, Activations: []*Activation{a}}
	go func() { defer close(released); _ = a.Lease.Release() }()
	auditExecutorWait(t, entered, "cleanup callback")
	read := make(chan struct{})
	go func() { defer close(read); _ = f.Snapshot() }()
	// .
	// .
	auditWaitFacility(t, "Snapshot holding facility state lock", func() bool {
		select {
		case <-read:
			return true
		default:
		}
		if f.mu.TryLock() {
			f.mu.Unlock()
			return false
		}
		return true
	})
	select {
	case <-read:
		release()
		auditExecutorWait(t, released, "release worker")
		return
	default:
	}
	observed := make(chan struct{})
	go func() { defer close(observed); f.Observe(obs("two.aiiospkg"), Policy{Revision: 1}) }()
	blocked := false
	select {
	case <-observed:
	case <-time.After(250 * time.Millisecond):
		blocked = true
	}
	release()
	auditExecutorWait(t, released, "release worker")
	auditExecutorWait(t, read, "snapshot worker")
	auditExecutorWait(t, observed, "Observe worker")
	if blocked {
		t.Error("cleanup of one plugin held the global facility lock and blocked Observe for another")
	}
}
