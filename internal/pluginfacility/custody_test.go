package pluginfacility

import (
	"errors"
	"strings"
	"testing"
)

// CLEANUP REQUESTED IS NOT RETIREMENT ESTABLISHED. The distinction is
// the whole point of the ledger: a facility that treats "Release was
// called" as "the resource is back" credits a reservation that is still
// spent, admits a replacement over a child that is still running, and
// tells the operator a plugin is gone while its root is still pinned.
func TestALeaseIsDischargedOnlyWhenEverythingCameBack(t *testing.T) {
	var order []string
	l := NewLease(7)
	l.Hold("binding", func() error { order = append(order, "binding"); return nil })
	l.Hold("runtime root", func() error { order = append(order, "root"); return nil })
	l.Hold("child", func() error { order = append(order, "child"); return nil })

	if l.Discharged() {
		t.Fatal("a lease that was never released is not discharged")
	}
	if err := l.Release(); err != nil {
		t.Fatalf("a clean release: %v", err)
	}
	// Last taken, first returned: a child is stopped before the root it
	// runs from is unpinned.
	if strings.Join(order, ",") != "child,root,binding" {
		t.Fatalf("released out of order: %v", order)
	}
	if !l.Discharged() || len(l.Residue()) != 0 || len(l.Holds()) != 0 {
		t.Fatalf("a clean release discharges: %v %v", l.Residue(), l.Holds())
	}
}

func TestAFailedReleaseKeepsCustodyAndSaysWhatIsStillHeld(t *testing.T) {
	reap := errors.New("child 48211 not yet reaped")
	stubborn := true
	l := NewLease(9)
	l.Hold("binding", func() error { return nil })
	l.Hold("runtime root", func() error { return nil })
	l.Hold("child", func() error {
		if stubborn {
			return reap
		}
		return nil
	})

	err := l.Release()
	if err == nil {
		t.Fatal("a release that did not complete reported success")
	}
	if !errors.Is(err, reap) {
		t.Fatalf("the reason must survive: %v", err)
	}
	if !strings.Contains(err.Error(), "retirement is not established") {
		t.Fatalf("the error must say what it means: %v", err)
	}
	// Release was REQUESTED — and that is not the question anything acts on.
	if !l.Released() {
		t.Fatal("release was requested")
	}
	if l.Discharged() {
		t.Fatal("a child that did not reap is not retirement established")
	}
	if got := l.Residue(); len(got) != 1 || !strings.Contains(got[0], "child") {
		t.Fatalf("the residue names what is still held: %v", got)
	}
	// The failed child keeps what it stands on: its root and its binding
	// stay held behind it, in the order they were taken.
	if holds := l.Holds(); strings.Join(holds, ",") != "binding,runtime root,child" {
		t.Fatalf("a failed release must retain what the failure stands on: %v", holds)
	}

	// The child reaps at last: a retry resumes at the failure, retires
	// it, and only then what it stood on. A pending retirement is
	// recoverable, not permanent.
	stubborn = false
	if err := l.Release(); err != nil {
		t.Fatalf("the retry: %v", err)
	}
	if !l.Discharged() || len(l.Residue()) != 0 {
		t.Fatalf("the retry must discharge: %v", l.Residue())
	}
}

func TestANilLeaseIsSafeAndHoldsNothing(t *testing.T) {
	var l *Lease
	if !l.Discharged() || l.Release() != nil || l.Released() || len(l.Holds()) != 0 || len(l.Residue()) != 0 {
		t.Fatal("a lease that never existed holds nothing")
	}
	l.Hold("x", nil) // must not panic
}
