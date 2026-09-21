package store

import (
	"path/filepath"
	"testing"
)

// .
// .
// .
// .
// .
func TestWorkPlanRoundTrip(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), "aii.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if err := s.StartWorkSession("ws_plan", "cs-4 round-trip"); err != nil {
		t.Fatal(err)
	}

	focus := "land the plan surface"
	next := "write render test"
	plan := "## Plan\n- [ ] schema columns → store → verb\n- cite ws_ IDs in subgoal lines"
	if err := s.UpdateWorkPlan("ws_plan", &focus, &next, &plan, nil, nil, nil); err != nil {
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

	// .
	if err := s.UpdateWorkPlan("ws_plan", nil, nil, nil, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	ws, _ = s.ActiveWorkSession()
	if ws.Focus != focus {
		t.Fatalf("nil update touched focus: %q", ws.Focus)
	}

	// .
	empty := ""
	if err := s.UpdateWorkPlan("ws_plan", nil, nil, &empty, nil, nil, nil); err != nil {
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
