package app

import (
	"strings"
	"testing"
)

// OMITTED IS NOT CLEARED. Every field on the plan surface obeys that
// rule and says so; state did not. It was read with the comma-ok
// discarded and written unconditionally, so any update that did not
// mention state erased it in passing — including the focus-only and
// steps-only calls a resident makes constantly.
func TestWorkUpdateDoesNotClearStateItWasNotGiven(t *testing.T) {
	a := standingApp(t)
	if err := a.store.StartWorkSession(t.Context(), "ws1", "an arc"); err != nil {
		t.Fatal(err)
	}
	if txt, failed := workCall(t, a, `{"action":"update","state":"deep in the parser"}`); failed {
		t.Fatalf("setup: %s", txt)
	}

	stateNow := func() string {
		t.Helper()
		ws, err := a.store.ActiveWorkSession()
		if err != nil || ws == nil {
			t.Fatalf("active session: %v %+v", err, ws)
		}
		return ws.State
	}

	// A focus-only update touches focus and nothing else.
	if txt, failed := workCall(t, a, `{"action":"update","focus":"the parser"}`); failed {
		t.Fatalf("focus-only update: %s", txt)
	}
	if got := stateNow(); got != "deep in the parser" {
		t.Errorf("a focus-only update erased state (now %q) — the resident loses its place for mentioning something else", got)
	}

	// A steps-only update is how a budget is declared; it must not cost
	// the session its state either.
	if _, failed := workCall(t, a, `{"action":"update","steps":7}`); failed {
		t.Fatal("a steps-only update against a live session must not fail")
	}
	if got := stateNow(); got != "deep in the parser" {
		t.Errorf("a steps-only update erased state (now %q)", got)
	}

	// A standing-only update, likewise.
	if txt, failed := workCall(t, a, `{"action":"update","standing":"holding"}`); failed {
		t.Fatalf("standing-only update: %s", txt)
	}
	if got := stateNow(); got != "deep in the parser" {
		t.Errorf("a standing-only update erased state (now %q)", got)
	}

	// And an EXPLICIT empty string still clears, because presence is
	// the rule — not emptiness.
	if txt, failed := workCall(t, a, `{"action":"update","state":""}`); failed {
		t.Fatalf("explicit clear: %s", txt)
	}
	if got := stateNow(); got != "" {
		t.Errorf("an explicit empty state must clear it, got %q", got)
	}
}

// A standing-only call has no "rest" to withhold, so reporting one was
// inventing a failure that did not happen.
func TestStandingOnlyUpdateWithoutASessionReportsNoPhantomRemainder(t *testing.T) {
	a := standingApp(t)

	txt, failed := workCall(t, a, `{"action":"update","standing":"waiting on the operator"}`)
	if failed {
		t.Fatalf("standing needs no session: %s", txt)
	}
	if strings.Contains(txt, "not applied") {
		t.Errorf("nothing else was asked for, yet the answer withheld something: %q", txt)
	}
	if !strings.Contains(txt, "Standing state updated") {
		t.Errorf("the answer must say what happened, got %q", txt)
	}
}

// THE METER RECORDS WHAT RAN. It reads the requested arguments, so
// counting before dispatch credited refused work: a spawn the guard
// turned away still recorded spawned=1, and a refused steps=7 still
// recorded predicted=7 — inflating the beta numbers and buying
// soft-cap runway for a declaration that never took effect.
func TestTheMeterCountsWhatRanNotWhatWasAsked(t *testing.T) {
	a := standingApp(t)

	// No work session exists, so these are refused at dispatch.
	if txt, failed := workCall(t, a, `{"action":"update","steps":7}`); !failed {
		t.Fatalf("setup: a steps-only update with no session must be refused, got %q", txt)
	}
	if a.turn.turnPredicted != 0 || a.turn.turnFirstPredicted != 0 {
		t.Errorf("a REFUSED steps=7 recorded predicted=%d/first=%d — the meter credited a declaration that never took effect",
			a.turn.turnPredicted, a.turn.turnFirstPredicted)
	}

	if txt, failed := workCall(t, a, `{"action":"spawn","description":"a child that never ran"}`); !failed {
		t.Fatalf("setup: a spawn with no session must be refused, got %q", txt)
	}
	if a.turn.turnSpawned != 0 {
		t.Errorf("a REFUSED spawn recorded spawned=%d — the turn's row now claims a child exists", a.turn.turnSpawned)
	}

	// CONTROL: a call that really runs is still counted, or this test
	// would pass just as happily against a meter that counts nothing.
	if err := a.store.StartWorkSession(t.Context(), "ws1", "an arc"); err != nil {
		t.Fatal(err)
	}
	if txt, failed := workCall(t, a, `{"action":"update","state":"running","steps":3}`); failed {
		t.Fatalf("control: %s", txt)
	}
	if a.turn.turnPredicted != 3 {
		t.Errorf("an EXECUTED steps=3 must be counted, got predicted=%d", a.turn.turnPredicted)
	}
	if a.turn.turnCalls == 0 {
		t.Error("executed calls must still reach the meter at all")
	}
}

// steps and independent are DECLARATIONS ABOUT A SESSION'S work. The
// verb stores neither — the meter reads them off the raw arguments —
// but that makes them session-bound, not standing-only. Leaving them
// out of the presence test let `standing + steps` with no session take
// the standing-only SUCCESS path, after which the meter counted a
// declaration that nothing had accepted.
func TestStandingPlusStepsWithoutASessionCreditsNothing(t *testing.T) {
	a := standingApp(t)

	txt, failed := workCall(t, a, `{"action":"update","standing":"waiting","steps":7}`)
	if !failed {
		t.Fatalf("steps is session-bound; with no session this call must fail: %q", txt)
	}
	if !strings.Contains(txt, "persists") {
		t.Errorf("the failure must still name the standing write that landed: %q", txt)
	}
	if a.turn.turnPredicted != 0 || a.turn.turnFirstPredicted != 0 {
		t.Errorf("a refused declaration was credited to the meter: predicted=%d first=%d", a.turn.turnPredicted, a.turn.turnFirstPredicted)
	}

	// The standing half really did persist — that is why the failure
	// has to name it rather than read as "nothing happened".
	got, err := a.store.StandingState()
	if err != nil {
		t.Fatal(err)
	}
	if got != "waiting" {
		t.Errorf("standing state = %q, want the value the error says persists", got)
	}
}
