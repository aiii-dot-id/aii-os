// .
// .
// .
// .
// .
// .

package pluginfacility

import (
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestAuditFailedChildRetainsItsDependencies(t *testing.T) {
	l := NewLease(1)
	reaped := false
	rootReleased, reservationReturned := 0, 0
	pending := errors.New("child not reaped")
	l.Hold("reservation", func() error { reservationReturned++; return nil })
	l.Hold("runtime root", func() error { rootReleased++; return nil })
	l.Hold("child", func() error {
		if !reaped {
			return pending
		}
		return nil
	})
	if err := l.Release(); !errors.Is(err, pending) {
		t.Fatalf("lost child failure: %v", err)
	}
	if rootReleased != 0 || reservationReturned != 0 {
		t.Errorf("unreaped child lost dependencies: root releases=%d reservation credits=%d", rootReleased, reservationReturned)
	}
	if want := []string{"reservation", "runtime root", "child"}; !reflect.DeepEqual(l.Holds(), want) {
		t.Errorf("pending dependencies forgotten: %v", l.Holds())
	}
	reaped = true
	if err := l.Release(); err != nil {
		t.Fatal(err)
	}
	if rootReleased != 1 || reservationReturned != 1 || !l.Discharged() {
		t.Fatalf("recovery did not release dependencies exactly once: root=%d reservation=%d discharged=%v", rootReleased, reservationReturned, l.Discharged())
	}
}

func TestAuditRepeatedResourceNamesCannotHideFailure(t *testing.T) {
	l := NewLease(2)
	pending := errors.New("second pin remains held")
	ready := false
	l.Hold("runtime pin", func() error { return nil })
	l.Hold("runtime pin", func() error {
		if !ready {
			return pending
		}
		return nil
	})
	if err := l.Release(); !errors.Is(err, pending) {
		t.Errorf("cleanup returned %v while resource remains held: %v", err, l.Holds())
	}
	if l.Discharged() {
		t.Error("unfinished cleanup discharged")
	}
	ready = true
	if err := l.Release(); err != nil {
		t.Fatal(err)
	}
	if !l.Discharged() {
		t.Fatal("successful retry did not discharge")
	}
}

func TestAuditLateResourceCannotRemainDischargedOrBePruned(t *testing.T) {
	i := newInst()
	i.add(1, RoleActive)
	old := i.add(2, RoleRetiring)
	if err := old.Lease.Release(); err != nil {
		t.Fatal(err)
	}
	released := false
	old.Lease.Hold("late child", func() error { released = true; return nil })
	if old.Lease.Discharged() || old.Settled() {
		t.Error("a lease holding a late resource still reports discharged/settled")
	}
	if i.CanAdmitCandidate() {
		t.Error("late child custody did not block a candidate")
	}
	i.Prune()
	found := false
	for _, a := range i.Activations {
		if a == old {
			found = true
		}
	}
	if !found && !released {
		t.Error("instance pruned an activation that still owns an unreleased late child")
	}
	// .
	if err := old.Lease.Release(); err != nil {
		t.Fatal(err)
	}
}

func TestAuditCleanupCannotBlockCustodyReadback(t *testing.T) {
	i := newInst()
	a := i.add(3, RoleRetiring)
	l := a.Lease
	entered, finish, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	l.Hold("blocked child", func() error { close(entered); <-finish; return nil })
	go func() { defer close(done); _ = l.Release() }()
	<-entered
	read := make(chan State, 1)
	go func() { read <- i.State() }()
	observed := false
	select {
	case state := <-read:
		observed = true
		if state != StateDraining {
			t.Errorf("in-flight cleanup state=%s, want draining", state)
		}
	case <-time.After(time.Second):
		t.Error("Instance.State blocked behind external cleanup callback")
	}
	close(finish)
	<-done
	// .
	if !observed {
		<-read
	}
}

func TestAuditRefusedCleanupBlocksThirdEngine(t *testing.T) {
	i := newInst()
	i.add(1, RoleActive)
	failed := i.add(2, RoleRefused)
	failed.Refusal = NewRefusal(i.ID, i.Version, 2, StageHealth, errors.New("candidate unhealthy"))
	pending := true
	failed.Lease.Hold("candidate child", func() error {
		if pending {
			return errors.New("not reaped")
		}
		return nil
	})
	if err := failed.Lease.Release(); err == nil {
		t.Fatal("fixture did not retain child")
	}
	if failed.Settled() {
		t.Fatal("fixture unexpectedly settled")
	}
	if i.CanAdmitCandidate() {
		t.Error("active engine plus refused-but-unreaped candidate permits a third engine")
	}
	pending = false
	if err := failed.Lease.Release(); err != nil {
		t.Fatal(err)
	}
	if !i.CanAdmitCandidate() {
		t.Error("successfully retired refusal still blocks next candidate")
	}
}
