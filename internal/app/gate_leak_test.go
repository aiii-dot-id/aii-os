package app

import (
	"context"
	"testing"
)

// .
// .
// .
// .
// .
// .
// .

func TestAFailedWakeReturnsTheGate(t *testing.T) {
	a := newSteerApp(t)

	if !a.TryBeginTurn() {
		t.Fatal("a fresh app could not take its own turn gate")
	}
	if _, err := a.wake(context.Background(), "system", "[timer] something fired"); err == nil {
		t.Fatal("wake succeeded without a live runtime")
	}
	// .
	// .
	if !a.TurnActive() {
		t.Fatal("wake released a gate it never took; the caller's own defer would release it twice")
	}
	a.releaseTurn()
	if a.TurnActive() {
		t.Fatal("the gate did not come back")
	}
}

// .
// .
func TestTheIdentityIsNotDeafenedByAFailedWake(t *testing.T) {
	a := newSteerApp(t)

	// .
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
	// .
	steered, err = a.AdmitParticipant("are you there?")
	if err != nil {
		t.Fatal(err)
	}
	if steered {
		t.Fatal("the identity is deaf: a new message steered into a phantom turn")
	}
	a.releaseTurn()
}
