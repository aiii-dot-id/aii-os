package project

import "testing"

// .
// .
// .
func TestReview3SupportedDoesNotAllowClose(t *testing.T) {
	m := NewManager(t.TempDir())
	p, err := m.Create("S", "d", "identity", nil, &Contract{Acceptance: []string{"the check"}}, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// .
	if _, err := m.RecordObservationByText(p.ID, "the check", "worker_report_only", "ws_x", "child said so", "ivy"); err != nil {
		t.Fatalf("record: %v", err)
	}
	prog, _ := m.Progress(p.ID)
	if prog.Items[0].State != AcceptanceSupported {
		t.Fatalf("want supported, got %q", prog.Items[0].State)
	}
	if prog.ClosureAllowed {
		t.Fatal("a merely-supported item must NOT allow close after tightening")
	}
	if prog.NextIndex != 0 {
		t.Fatalf("the supported item is the next action, got NextIndex=%d", prog.NextIndex)
	}
	if _, err := m.SetState(p.ID, "closed"); err == nil {
		t.Fatal("close must be refused while an item is only supported")
	}
	// .
	if _, err := m.RecordObservationByText(p.ID, "the check", "locally_verified", "ws_v", "ran it", "ivy"); err != nil {
		t.Fatalf("verify: %v", err)
	}
	if _, err := m.SetState(p.ID, "closed"); err != nil {
		t.Fatalf("a verified item should close: %v", err)
	}
}
