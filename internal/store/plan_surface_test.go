package store

import (
	"path/filepath"
	"testing"
)

// TestWorkPlanRoundTrip pins CS-4's plan surface: fields write via
// UpdateWorkPlan with set-only semantics (nil = untouched, set
// pointer = write including empty-string clear), and
// ActiveWorkSession returns them for verbatim rendering. The
// substrate stores and renders; it never interprets (doctrine §1).
func TestWorkPlanRoundTrip(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), "aii.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if err := s.StartWorkSession(t.Context(), "ws_plan", "cs-4 round-trip"); err != nil {
		t.Fatal(err)
	}

	focus := "land CS-4 plan surface"
	next := "write render test"
	plan := "## Plan\n- [ ] schema columns → store → verb\n- cite ws_ IDs in subgoal lines"
	if err := s.UpdateWorkPlan(t.Context(), "ws_plan", &focus, &next, &plan, nil, nil, nil); err != nil {
		t.Fatal(err)
	}

	ws, err := s.ActiveWorkSession()
	if err != nil {
		t.Fatal(err)
	}
	if ws == nil {
		t.Fatal("no active session")
	}
	if ws.Focus != focus || ws.NextMove != next || ws.Plan != plan {
		t.Fatalf("plan round-trip diverged:\nfocus %q\nnext %q\nplan %q", ws.Focus, ws.NextMove, ws.Plan)
	}

	// Set-only: nil leaves the field untouched.
	if err := s.UpdateWorkPlan(t.Context(), "ws_plan", nil, nil, nil, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	ws, _ = s.ActiveWorkSession()
	if ws.Focus != focus {
		t.Fatalf("nil update touched focus: %q", ws.Focus)
	}

	// Clear: set pointer to empty string writes the empty value.
	empty := ""
	if err := s.UpdateWorkPlan(t.Context(), "ws_plan", nil, nil, &empty, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	ws, _ = s.ActiveWorkSession()
	if ws.Plan != "" {
		t.Fatalf("empty-string set did not clear plan: %q", ws.Plan)
	}
	if ws.Focus != focus || ws.NextMove != next {
		t.Fatalf("clearing plan touched siblings: focus %q next %q", ws.Focus, ws.NextMove)
	}
}
