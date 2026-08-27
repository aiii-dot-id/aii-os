package app

import (
	"errors"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
)

// steering_facility_test.go — the 2026-08-26 outage, as a sequence.
//
// 13:08:37 a self_model pass took the turn gate. 13:10:09 the operator
// spoke; admit() steered the words into the pass — which never reads
// the steer queue (only the conversation loop drains, at tool
// boundaries) — and the message sat "1 pending" for six hours while
// its author read silence and called the OS fundamentally broken. He
// was right. These tests run that afternoon's exact sequence against
// the fix: a facility hold refuses the steer structurally, and words a
// turn accepted but never drained become their own turn at release.

func gateApp() *App {
	g := make(chan struct{}, 1)
	g <- struct{}{}
	return &App{turnGate: g}
}

// THE INCIDENT'S SEQUENCE, end to end at the gate level.
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
	// The pass ended; the same words must now open their own turn.
	steered, err = a.AdmitOperator("Summarize your findings for an implementation agent.")
	if steered || err != nil {
		t.Fatalf("after the pass the message must take the gate (steered=%v err=%v)", steered, err)
	}
	a.releaseTurn()
}

// Steering into a CONVERSATION stays exactly what it was: delivered at
// the next tool boundary, inside the running turn.
func TestConversationalHoldStillSteers(t *testing.T) {
	a := gateApp()
	if steered, err := a.AdmitOperator("open a turn"); steered || err != nil {
		t.Fatalf("first message did not take the gate: steered=%v err=%v", steered, err)
	}
	steered, err := a.AdmitOperator("mid-turn correction")
	if !steered || err != nil {
		t.Fatalf("second message did not steer: steered=%v err=%v", steered, err)
	}
	// The turn ends WITHOUT a tool boundary (a plain-text answer): the
	// correction is not abandoned — it flushes into its own turn.
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

// A steer the turn DID hear is done: nothing to flush at release.
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

// EndTurn clears the facility mark: the next hold is judged on its own.
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
