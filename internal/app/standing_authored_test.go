package app

import (
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/conversation"
)

// backdateStanding sets the stored authoring time, as an old row has it.
func backdateStanding(t *testing.T, a *App, ms int64) {
	t.Helper()
	if _, err := a.store.DB().Exec(`UPDATE standing_state SET updated_ms = ? WHERE singleton_id = 'current'`, ms); err != nil {
		t.Fatal(err)
	}
}

// A fixed date, from the row: the text is the identity's, unchanged, and
// the heading says the day it was authored. A row never stamped (0) says
// no date rather than 1970.
func TestStandingStateRendersTheDateItWasAuthored(t *testing.T) {
	a := standingApp(t)
	const text = "waiting on the reviewer's word\nthen the port"
	if err := a.store.SetStandingState(text); err != nil {
		t.Fatal(err)
	}
	backdateStanding(t, a, time.Date(2026, 9, 15, 23, 30, 0, 0, time.UTC).UnixMilli())
	ws, err := a.buildWorkState()
	if err != nil {
		t.Fatal(err)
	}
	want := "### Standing state (yours, authored 2026-09-15 — re-author when it stops being true)\n" + text
	if !strings.Contains(ws, want) {
		t.Fatalf("the standing state does not say when it was authored, or its text moved; want\n%s\nin\n%s", want, ws)
	}

	backdateStanding(t, a, 0)
	ws, err = a.buildWorkState()
	if err != nil {
		t.Fatal(err)
	}
	if want := "### Standing state (yours — re-author when it stops being true)\n" + text; !strings.Contains(ws, want) {
		t.Fatalf("a never-stamped standing state should render with no date; want\n%s\nin\n%s", want, ws)
	}
	if strings.Contains(ws, "1970") || strings.Contains(ws, "authored") {
		t.Fatalf("a never-stamped standing state rendered a date:\n%s", ws)
	}
}

// The date is a pure function of the stored row, never an age ("N days
// ago"), so the working state holds still between turns: two consecutive
// real turns send the same system message and the same standing bytes in
// their blocks.
func TestADatedStandingStateHoldsStillBetweenTurns(t *testing.T) {
	app, obs := promptCacheApp(t)
	if err := app.store.SetStandingState("waiting on the review"); err != nil {
		t.Fatal(err)
	}
	backdateStanding(t, app, time.Date(2026, 9, 15, 8, 0, 0, 0, time.UTC).UnixMilli())
	operatorTurn(t, app, "first question")
	operatorTurn(t, app, "second question")
	reqs := obs.snapshot()
	if len(reqs) != 2 {
		t.Fatalf("saw %d requests, want 2", len(reqs))
	}
	first, second := wireMessages(t, reqs[0]), wireMessages(t, reqs[1])
	if first[0].Content != second[0].Content {
		t.Fatal("the system message moved between two turns with nothing re-authored")
	}
	ring4 := func(msgs []wireMessage) string {
		last := msgs[len(msgs)-1].Content
		at := strings.Index(last, conversation.TurnOpen+"\n# What Is Right in Front of You")
		if at < 0 {
			t.Fatalf("no working state in the block: %q", last)
		}
		block := last[at:]
		end := strings.Index(block, "waiting on the review")
		if end < 0 {
			t.Fatalf("the standing state is not in the block: %q", block)
		}
		return block[:end+len("waiting on the review")]
	}
	a, b := ring4(first), ring4(second)
	if a != b {
		t.Fatalf("the working state moved between turns:\n%q\n%q", a, b)
	}
	if !strings.Contains(a, "authored 2026-09-15") {
		t.Fatalf("the block does not carry the authoring date: %q", a)
	}
}
