package conversation

import (
	"strings"
	"testing"
)

func TestTheCheckpointIsArmedOnRoundsAtTheShippedDefaults(t *testing.T) {
	client, res := runCapTurn(t, 29, 30, func() int { return 0 })
	if !res.ContinuedAtRounds || res.ContinuedAtCap || res.ExhaustedBudget {
		t.Fatalf("a turn of one call a round did not end at its round checkpoint: %+v", res)
	}
	// Armed at the end of round 25 (five rounds left: four of grace and
	// the ceiling's), ended after round 29: 29 tool requests and the final.
	if got := len(client.seen); got != 30 {
		t.Errorf("turn took %d requests, want 30", got)
	}
	if got := checkpointCount(client); got != 1 {
		t.Errorf("checkpoint ask appeared %d times, want 1", got)
	}
	all := joined(client.seen[len(client.seen)-1])
	if !strings.Contains(all, "[This turn has used 25 of its 30 tool rounds. Checkpoint NOW") || !strings.Contains(all, "You have 4 more rounds") {
		t.Errorf("the ask does not say the rounds armed it")
	}
	// The countdown counts the rounds before the checkpoint's end, not the
	// ceiling's: two rounds are left after round 27, none after round 29.
	if !strings.Contains(all, "[2 rounds remain in this turn") || !strings.Contains(all, "[1 round remains in this turn") {
		t.Errorf("the countdown is not counted to the checkpoint's end")
	}
	want := "[This turn ended at its checkpoint after 29 tool calls, before its limit of 30 rounds; the work continues automatically in a fresh turn.]"
	if len(res.Notes) != 1 || res.Notes[0] != want {
		t.Errorf("the ending = %q, want %q", res.Notes, want)
	}
	if got := said(res, res.NothingContinues("no work session is active")); got != "done\n\n"+nothingContinuesLine(29, "no work session is active") {
		t.Errorf("a caller that continues nothing closes it on why: %q", got)
	}
}

// A turn that reaches the ceiling itself, with no checkpoint that could
// fit before it, promises what a budget ending promises, and the caller
// that schedules nothing takes the promise back (NothingContinues).
func TestTheRoundCeilingContinuesAsABudgetDoes(t *testing.T) {
	_, res := runCheckpointTurn(t, 4, 4, func() int { return 0 }, Checkpoint{GraceRounds: 4})
	if !res.ExhaustedBudget || res.ContinuedAtRounds || res.ContinuedAtCap {
		t.Fatalf("result = %+v", res)
	}
	want := "[This turn reached its limit of 4 rounds and was finished without further tools; while its work session is still active, the work continues automatically in a fresh turn.]"
	if len(res.Notes) != 1 || res.Notes[0] != want {
		t.Fatalf("the ceiling's line = %q, want %q", res.Notes, want)
	}
	if got := said(res, res.NothingContinues("no work session is active")); got != "done\n\n"+nothingContinuesLine(4, "no work session is active") {
		t.Errorf("a caller that continues nothing closes it on why: %q", got)
	}
}
