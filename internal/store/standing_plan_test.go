package store

import (
	"path/filepath"
	"testing"
)

func TestRecentPlanSurvivesSessionClose(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), "aii.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	// No plan anywhere: false, not an error.
	if _, ok, err := s.RecentPlan(); err != nil || ok {
		t.Fatalf("empty store: ok=%v err=%v", ok, err)
	}

	// Author a plan, close its session.
	if err := s.StartWorkSession(t.Context(), "ws_a", "first arc"); err != nil {
		t.Fatal(err)
	}
	focus := "verify the swap from bytes"
	if err := s.UpdateWorkPlan(t.Context(), "ws_a", &focus, nil, nil, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.DeliverWorkSession(t.Context(), "ws_a", "done", "", ""); err != nil {
		t.Fatal(err)
	}

	// Active session gone — the plan still renders.
	rp, ok, err := s.RecentPlan()
	if err != nil || !ok {
		t.Fatalf("after close: ok=%v err=%v", ok, err)
	}
	if rp.Focus != focus {
		t.Fatalf("standing plan lost focus: %q", rp.Focus)
	}

	// A newer plan replaces it in the render (rowid DESC).
	if err := s.StartWorkSession(t.Context(), "ws_b", "second arc"); err != nil {
		t.Fatal(err)
	}
	focus2 := "second arc focus"
	if err := s.UpdateWorkPlan(t.Context(), "ws_b", &focus2, nil, nil, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	rp, _, _ = s.RecentPlan()
	if rp.Focus != focus2 {
		t.Fatalf("newest plan did not win: %q", rp.Focus)
	}
}

func TestRecentPlanExcludesSubagentSessions(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), "aii.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	// Main thread authors a plan.
	if err := s.StartWorkSession(t.Context(), "ws_main", "main arc"); err != nil {
		t.Fatal(err)
	}
	focus := "the main thread's own focus"
	if err := s.UpdateWorkPlan(t.Context(), "ws_main", &focus, nil, nil, nil, nil, nil); err != nil {
		t.Fatal(err)
	}

	// A sub-agent session, NEWER by rowid, authors its own plan.
	queueChild(t, s, "ws_sub", "CLAIMED")
	if err := s.StartWorkSession(t.Context(), "ws_sub", SubagentDescription("survey forks")); err != nil {
		t.Fatal(err)
	}
	subFocus := "the sub-agent's own focus"
	if err := s.UpdateWorkPlan(t.Context(), "ws_sub", &subFocus, nil, nil, nil, nil, nil); err != nil {
		t.Fatal(err)
	}

	// The standing plan is still the main thread's.
	rp, ok, err := s.RecentPlan()
	if err != nil || !ok {
		t.Fatalf("standing plan: ok=%v err=%v", ok, err)
	}
	if rp.ID != "ws_main" {
		t.Fatalf("sub-agent plan leaked into the standing plan: got session %q (focus %q)", rp.ID, rp.Focus)
	}

	// Sub-agent only, no main-thread plan anywhere: no standing plan.
	if err := s.DeliverWorkSession(t.Context(), "ws_main", "done", "", ""); err != nil {
		t.Fatal(err)
	}
	s2, ok2, err2 := s.RecentPlan()
	if err2 != nil {
		t.Fatal(err2)
	}
	if ok2 && s2.ID != "ws_main" {
		t.Fatalf("expected no or only main-thread standing plan, got %q", s2.ID)
	}
}
