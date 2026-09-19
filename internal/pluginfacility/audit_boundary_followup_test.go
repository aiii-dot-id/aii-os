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
)

// .
// .
func TestAuditBoundaryLateFailedHoldKeepsCleanupOwner(t *testing.T) {
	for _, mode := range []string{"success", "error", "panic"} {
		t.Run(mode, func(t *testing.T) {
			i := newInst()
			i.add(1, RoleActive)
			a := i.add(2, RoleRetiring)
			if err := a.Lease.Release(); err != nil {
				t.Fatal(err)
			}
			i.Prune()
			ready := false
			calls := 0
			a.Lease.Hold("late child", func() error {
				calls++
				if ready || mode == "success" {
					return nil
				}
				if mode == "panic" {
					panic("late stop panicked")
				}
				return errors.New("late child not yet reaped")
			})
			owned := false
			for _, known := range i.Activations {
				if known == a {
					owned = true
				}
			}
			pending := !a.Lease.Discharged()
			if pending && !owned {
				t.Errorf("late %s cleanup remains owed after its activation was pruned: calls=%d residue=%v", mode, calls, a.Lease.Residue())
			}
			// .
			ready = true
			if err := a.Lease.Release(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// .
// .
// .
func TestAuditBoundaryWithdrawalPreventsInFlightRedirectEffect(t *testing.T) {
	for _, change := range []string{"policy", "uninstall"} {
		t.Run(change, func(t *testing.T) {
			rt := &auditExecutorRuntime{fakeRuntime: newFakeRuntime()}
			entered, published := make(chan struct{}), make(chan struct{})
			before, releaseBefore := auditExecutorBarrier()
			after, releaseAfter := auditExecutorBarrier()
			var withdrawn, reachable, late atomic.Bool
			rt.redirectHook = func(_ Running, to Running) error {
				close(entered)
				<-before
				// .
				// .
				authorized := to.(*fakeRunning).lease.Authorized()
				if authorized {
					reachable.Store(true)
					late.Store(withdrawn.Load())
				}
				close(published)
				<-after
				if !authorized {
					return errors.New("withdrawn before it was published")
				}
				return nil
			}
			rt.stopHook = func(ctx context.Context, r Running) (Retirement, error) {
				reachable.Store(false)
				return rt.fakeRuntime.Stop(ctx, r)
			}
			f := auditFacility(t, rt)
			defer releaseBefore()
			defer releaseAfter()
			set := []Observed{{ID: "id.example.p", Package: "one", Hash: "sha256:aa"}}
			f.Observe(set, Policy{Revision: 1})
			auditExecutorWait(t, entered, "redirect after commit question")
			if change == "policy" {
				f.Observe(set, Policy{Revision: 2, Safe: true})
			} else {
				f.Observe(nil, Policy{Revision: 2})
			}
			withdrawn.Store(true)
			releaseBefore()
			auditExecutorWait(t, published, "synthetic redirect publication")
			if late.Load() && reachable.Load() {
				t.Errorf("route became reachable after %s Observe returned; post-Redirect cleanup has not run", change)
			}
			releaseAfter()
			auditWaitFacility(t, "cleanup after redirect", func() bool { return strings.Contains(strings.Join(rt.seen(), ","), "stop:one") })
		})
	}
}
