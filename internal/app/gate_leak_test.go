package app

import (
	"context"
	"testing"
)

// A leaked turn gate does not fail loudly — it makes the identity DEAF.
// TurnActive stays true forever, so every message steers into a turn
// that does not exist, no new turn can start, metabolism defers on every
// pass, and only a restart recovers it.
//
// wake() used to release a gate it never took, and its "not live" check
// returned before the defer was armed. Whoever takes it releases it.

func TestAFailedWakeReturnsTheGate(t *testing.T) {
	a := newSteerApp(t) // no conv/composer/engine: wake refuses immediately

	if !a.TryBeginTurn() {
		t.Fatal("a fresh app could not take its own turn gate")
	}
	if _, err := a.wake(context.Background(), "system", "[timer] something fired"); err == nil {
		t.Fatal("wake succeeded without a live runtime")
	}
	// The caller still holds it — wake must not have released what it
	// did not take.
	if !a.TurnActive() {
		t.Fatal("wake released a gate it never took; the caller's own defer would release it twice")
	}
	a.releaseTurn()
	if a.TurnActive() {
		t.Fatal("the gate did not come back")
	}
}

// The whole point, stated as the symptom: after a failed wake the
// identity can still be spoken to.
func TestTheIdentityIsNotDeafenedByAFailedWake(t *testing.T) {
	a := newSteerApp(t)

	// Exactly what carryInbound does: admit, then wake, then release.
	steered, err := a.AdmitParticipant("[messages] someone wrote")
	if err != nil || steered {
		t.Fatalf("admission did not take the gate: steered=%v err=%v", steered, err)
	}
	func() {
		defer a.releaseTurn()
		_, _ = a.wake(context.Background(), "participant", "[messages] someone wrote")
	}()

	if a.TurnActive() {
		t.Fatal("the gate leaked — from here every message steers into a turn that does not exist")
	}
	// And a later message opens a real turn rather than steering.
	steered, err = a.AdmitParticipant("are you there?")
	if err != nil {
		t.Fatal(err)
	}
	if steered {
		t.Fatal("the identity is deaf: a new message steered into a phantom turn")
	}
	a.releaseTurn()
}
