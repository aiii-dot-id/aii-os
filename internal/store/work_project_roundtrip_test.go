package store

import "testing"

// .
// .
// .
// .
// .
func TestWorkSessionProjectRoundTrip(t *testing.T) {
	s, err := NewMemory()
	if err != nil {
		t.Fatalf("NewMemory: %v", err)
	}
	defer s.Close()

	// .
	s.SetActiveProject("probe")
	if err := s.StartWorkSession("ctrl-pos", "sub-agent: session under a focused project"); err != nil {
		t.Fatalf("StartWorkSession(pos): %v", err)
	}
	s.SetActiveProject("")

	live, err := s.LiveSubagentSessions()
	if err != nil {
		t.Fatalf("LiveSubagentSessions: %v", err)
	}
	if len(live) != 1 {
		t.Fatalf("live sessions = %d, want 1", len(live))
	}
	if live[0].Project != "probe" {
		t.Fatalf("live session Project = %q, want %q", live[0].Project, "probe")
	}

	// .
	if err := s.DeliverWorkSession("ctrl-pos", "delivered under probe", "", ""); err != nil {
		t.Fatalf("DeliverWorkSession: %v", err)
	}
	delivered, err := s.RecentDeliveredSubagents(10)
	if err != nil {
		t.Fatalf("RecentDeliveredSubagents: %v", err)
	}
	if len(delivered) != 1 || delivered[0].ID != "ctrl-pos" {
		t.Fatalf("delivered = %+v, want ctrl-pos only", delivered)
	}
	if delivered[0].Project != "probe" {
		t.Fatalf("delivered session Project = %q, want %q", delivered[0].Project, "probe")
	}

	// .
	if err := s.StartWorkSession("ctrl-neg", "sub-agent: session without a project"); err != nil {
		t.Fatalf("StartWorkSession(neg): %v", err)
	}
	live2, err := s.LiveSubagentSessions()
	if err != nil {
		t.Fatalf("LiveSubagentSessions(neg): %v", err)
	}
	if len(live2) != 1 || live2[0].ID != "ctrl-neg" {
		t.Fatalf("live after deliver = %+v, want ctrl-neg only", live2)
	}
	if live2[0].Project != "" {
		t.Fatalf("negative control Project = %q, want empty", live2[0].Project)
	}
}

func TestActiveWorkSessionIsNewestAndSubagentDescriptionRoundTrips(t *testing.T) {
	s, err := NewMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	description := SubagentDescription("inspect the queue")
	if got := SubagentGoal(description); got != "inspect the queue" {
		t.Fatalf("goal = %q", got)
	}
	if err := s.StartWorkSession("older", "ordinary work"); err != nil {
		t.Fatal(err)
	}
	if err := s.StartWorkSession("newer", description); err != nil {
		t.Fatal(err)
	}
	active, err := s.ActiveWorkSession()
	if err != nil {
		t.Fatal(err)
	}
	if active == nil || active.ID != "newer" {
		t.Fatalf("active = %+v, want newest", active)
	}
}
