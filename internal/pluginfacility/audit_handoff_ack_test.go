// .
// .
// .
// .
// .
// .

package pluginfacility

import (
	"errors"
	"testing"
)

// .
// .
func TestAuditLateHandoffAcknowledgmentRetainsProducerResponsibility(t *testing.T) {
	for _, mode := range []string{"success", "error", "panic"} {
		t.Run(mode, func(t *testing.T) {
			i := newInst()
			i.add(1, RoleActive)
			a := i.add(2, RoleRetiring)
			if err := a.Lease.Release(); err != nil {
				t.Fatal(err)
			}
			i.Prune()
			calls := 0
			ready := false
			returned := false
			giveBack := func() error {
				calls++
				if ready || mode == "success" {
					returned = true
					return nil
				}
				if mode == "panic" {
					panic("synthetic pending cleanup")
				}
				return errors.New("synthetic pending cleanup")
			}
			err := a.Lease.Hold("late resource", giveBack)
			var sealed *SealedHoldError
			if !errors.As(err, &sealed) || !errors.Is(err, ErrSealed) {
				t.Fatalf("handoff did not acknowledge refusal: %v", err)
			}
			if sealed.Released != returned || calls != 1 || !a.Lease.Discharged() || len(a.Lease.Holds()) != 0 {
				t.Fatalf("incorrect transfer acknowledgment: err=%v returned=%v calls=%d", err, returned, calls)
			}
			if !sealed.Released {
				if sealed.Err == nil {
					t.Fatal("failed release cause hidden")
				}
				ready = true
				if err := giveBack(); err != nil {
					t.Fatal(err)
				}
			}
			if !returned {
				t.Fatal("producer lost its resource")
			}
			before := calls
			if err := a.Lease.Release(); err != nil || calls != before {
				t.Fatalf("ledger retried a refused transfer: %v", err)
			}
		})
	}
}
