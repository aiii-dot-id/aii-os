package project

import (
	"strings"
	"testing"
)

// .
// .
func TestReview2EvidenceFollowsTextAcrossReorder(t *testing.T) {
	m := NewManager(t.TempDir())
	p, err := m.Create("R", "d", "identity", nil, &Contract{
		Acceptance: []string{"alpha done", "beta done"},
	}, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := m.RecordObservationByText(p.ID, "beta done", "locally_verified", "ws_b", "checked", "ivy"); err != nil {
		t.Fatalf("record: %v", err)
	}
	// .
	if _, err := m.ApplyPatch(p.ID, nil, nil, nil, nil, &Contract{
		Acceptance: []string{"beta done", "alpha done"},
	}, nil); err != nil {
		t.Fatalf("reorder: %v", err)
	}
	prog, _ := m.Progress(p.ID)
	// .
	if prog.Items[0].Text != "beta done" || prog.Items[0].State != AcceptanceVerified {
		t.Fatalf("evidence did not follow text across reorder: item0=%+v", prog.Items[0])
	}
	card := RenderProjectCard("R", "", Contract{Acceptance: []string{"beta done", "alpha done"}}, prog)
	if strings.Contains(card, "none recorded yet") && strings.Contains(card, "beta done") {
		// .
		if prog.NextIndex >= 0 && prog.Items[prog.NextIndex].Text == "beta done" {
			t.Fatalf("card falsely claims no evidence for a verified item:\n%s", card)
		}
	}
}

// .
// .
func TestReview2DuplicateAcceptanceRefused(t *testing.T) {
	m := NewManager(t.TempDir())
	_, err := m.Create("D", "d", "identity", nil, &Contract{
		Acceptance: []string{"deploy the service", "deploy the service"},
	}, nil)
	if err == nil {
		t.Fatal("duplicate acceptance items must be refused at create")
	}
	if !strings.Contains(err.Error(), "duplicate acceptance") {
		t.Fatalf("the refusal must name the problem: %v", err)
	}
	// .
	p, err := m.Create("D2", "d", "identity", nil, &Contract{Acceptance: []string{"one thing"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.ApplyPatch(p.ID, nil, nil, nil, nil, &Contract{
		Acceptance: []string{"same", "same"},
	}, nil); err == nil {
		t.Fatal("duplicate acceptance items must be refused on update too")
	}
}

// .
// .
func TestReview2EvidencePathRejectsWaivedAndUnknown(t *testing.T) {
	m := NewManager(t.TempDir())
	p, _ := m.Create("W", "d", "identity", nil, &Contract{Acceptance: []string{"the check"}}, nil)
	if _, err := m.RecordObservationByText(p.ID, "the check", "waived", "", "", "ivy"); err == nil {
		t.Fatal("the evidence path must not accept a reasonless waived class")
	}
	if _, err := m.RecordObservationByText(p.ID, "the check", "totally_made_up", "", "", "ivy"); err == nil {
		t.Fatal("an unknown class must be refused")
	}
	// .
	if _, err := m.WaiveItemByText(p.ID, "the check", "ivy", "out of scope"); err != nil {
		t.Fatalf("WaiveItem: %v", err)
	}
}
