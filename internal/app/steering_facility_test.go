package app

import (
	"errors"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
)

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

func gateApp() *App {
	g := make(chan struct{}, 1)
	g <- struct{}{}
	return &App{turnGate: g}
}

// .
func TestFacilityHoldQueuesInsteadOfSwallowing(t *testing.T) {
	a := gateApp()
	fg := facilityGate{a}
	if !fg.TryBeginTurn() {
		t.Fatal("facility could not take a free gate")
	}
	steered, err := a.AdmitOperator("Summarize your findings for an implementation agent.")
	if steered {
		t.Fatal("steered into a facility pass — these words would never be read")
	}
	if !errors.Is(err, dashboard.ErrBusyInternal) {
		t.Fatalf("want ErrBusyInternal so the dashboard can park the message, got: %v", err)
	}
	if n := len(a.PendingSteers()); n != 0 {
		t.Fatalf("%d message(s) queued into a facility turn anyway", n)
	}
	fg.EndTurn()
	// .
	steered, err = a.AdmitOperator("Summarize your findings for an implementation agent.")
	if steered || err != nil {
		t.Fatalf("after the pass the message must take the gate (steered=%v err=%v)", steered, err)
	}
	a.releaseTurn()
}

// .
// .
func TestConversationalHoldStillSteers(t *testing.T) {
	a := gateApp()
	if steered, err := a.AdmitOperator("open a turn"); steered || err != nil {
		t.Fatalf("first message did not take the gate: steered=%v err=%v", steered, err)
	}
	steered, err := a.AdmitOperator("mid-turn correction")
	if !steered || err != nil {
		t.Fatalf("second message did not steer: steered=%v err=%v", steered, err)
	}
	// .
	// .
	var flushed [][]steerEntry
	a.steerFlush = func(e []steerEntry) { flushed = append(flushed, e) }
	a.releaseTurn()
	if len(flushed) != 1 || len(flushed[0]) != 1 || flushed[0][0].content != "mid-turn correction" {
		t.Fatalf("undrained steer did not flush to its own turn: %+v", flushed)
	}
	if a.TurnActive() {
		t.Fatal("gate not returned")
	}
}

// .
func TestDrainedSteersDoNotFlush(t *testing.T) {
	a := gateApp()
	if steered, err := a.AdmitOperator("open"); steered || err != nil {
		t.Fatalf("open: steered=%v err=%v", steered, err)
	}
	if steered, err := a.AdmitOperator("heard mid-turn"); !steered || err != nil {
		t.Fatalf("steer: steered=%v err=%v", steered, err)
	}
	if got := a.DrainSteering(); len(got) != 1 || got[0] != "heard mid-turn" {
		t.Fatalf("drain returned %v", got)
	}
	var flushes int
	a.steerFlush = func([]steerEntry) { flushes++ }
	a.releaseTurn()
	if flushes != 0 {
		t.Fatalf("a drained steer flushed again: %d", flushes)
	}
}

// .
func TestFacilityMarkDoesNotOutliveItsPass(t *testing.T) {
	a := gateApp()
	fg := facilityGate{a}
	if !fg.TryBeginTurn() {
		t.Fatal("take")
	}
	fg.EndTurn()
	if steered, err := a.AdmitOperator("open"); steered || err != nil {
		t.Fatalf("open after pass: steered=%v err=%v", steered, err)
	}
	steered, err := a.AdmitOperator("this is a conversation now")
	if !steered || err != nil {
		t.Fatalf("stale facility mark bounced an ordinary steer: steered=%v err=%v", steered, err)
	}
	a.steerFlush = func([]steerEntry) {}
	a.releaseTurn()
}
