package app

import (
	"strings"
	"testing"
)

// .
// .
// .
// .
// .
func TestWorkUpdateDoesNotClearStateItWasNotGiven(t *testing.T) {
	a := standingApp(t)
	if err := a.store.StartWorkSession("ws1", "an arc"); err != nil {
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

	// .
	if txt, failed := workCall(t, a, `{"action":"update","focus":"the parser"}`); failed {
		t.Fatalf("focus-only update: %s", txt)
	}
	if got := stateNow(); got != "deep in the parser" {
		t.Errorf("a focus-only update erased state (now %q) — the resident loses its place for mentioning something else", got)
	}

	// .
	// .
	if _, failed := workCall(t, a, `{"action":"update","steps":7}`); failed {
		t.Fatal("a steps-only update against a live session must not fail")
	}
	if got := stateNow(); got != "deep in the parser" {
		t.Errorf("a steps-only update erased state (now %q)", got)
	}

	// .
	if txt, failed := workCall(t, a, `{"action":"update","standing":"holding"}`); failed {
		t.Fatalf("standing-only update: %s", txt)
	}
	if got := stateNow(); got != "deep in the parser" {
		t.Errorf("a standing-only update erased state (now %q)", got)
	}

	// .
	// .
	if txt, failed := workCall(t, a, `{"action":"update","state":""}`); failed {
		t.Fatalf("explicit clear: %s", txt)
	}
	if got := stateNow(); got != "" {
		t.Errorf("an explicit empty state must clear it, got %q", got)
	}
}

// .
// .
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

// .
// .
// .
// .
// .
// .
func TestTheMeterCountsWhatRanNotWhatWasAsked(t *testing.T) {
	a := standingApp(t)

	// .
	if txt, failed := workCall(t, a, `{"action":"update","steps":7}`); !failed {
		t.Fatalf("setup: a steps-only update with no session must be refused, got %q", txt)
	}
	if a.turnPredicted != 0 || a.turnFirstPredicted != 0 {
		t.Errorf("a REFUSED steps=7 recorded predicted=%d/first=%d — the meter credited a declaration that never took effect",
			a.turnPredicted, a.turnFirstPredicted)
	}

	if txt, failed := workCall(t, a, `{"action":"spawn","description":"a child that never ran"}`); !failed {
		t.Fatalf("setup: a spawn with no session must be refused, got %q", txt)
	}
	if a.turnSpawned != 0 {
		t.Errorf("a REFUSED spawn recorded spawned=%d — the fan-out nudge now believes a child exists", a.turnSpawned)
	}

	// .
	// .
	if err := a.store.StartWorkSession("ws1", "an arc"); err != nil {
		t.Fatal(err)
	}
	if txt, failed := workCall(t, a, `{"action":"update","state":"running","steps":3}`); failed {
		t.Fatalf("control: %s", txt)
	}
	if a.turnPredicted != 3 {
		t.Errorf("an EXECUTED steps=3 must be counted, got predicted=%d", a.turnPredicted)
	}
	if a.turnCalls == 0 {
		t.Error("executed calls must still reach the meter at all")
	}
}

// .
// .
// .
// .
// .
// .
func TestStandingPlusStepsWithoutASessionCreditsNothing(t *testing.T) {
	a := standingApp(t)

	txt, failed := workCall(t, a, `{"action":"update","standing":"waiting","steps":7}`)
	if !failed {
		t.Fatalf("steps is session-bound; with no session this call must fail: %q", txt)
	}
	if !strings.Contains(txt, "persists") {
		t.Errorf("the failure must still name the standing write that landed: %q", txt)
	}
	if a.turnPredicted != 0 || a.turnFirstPredicted != 0 {
		t.Errorf("a refused declaration was credited to the meter: predicted=%d first=%d", a.turnPredicted, a.turnFirstPredicted)
	}

	// .
	// .
	got, err := a.store.StandingState()
	if err != nil {
		t.Fatal(err)
	}
	if got != "waiting" {
		t.Errorf("standing state = %q, want the value the error says persists", got)
	}
}
