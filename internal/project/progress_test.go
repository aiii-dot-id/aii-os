package project

import (
	"strings"
	"testing"
)

// .
// .
// .

func TestDeriveAcceptanceStates(t *testing.T) {
	c := Contract{
		Outcome:    "ship the beta on three platforms",
		Acceptance: []string{"windows installs clean", "the dmg is stapled", "the deb registers"},
	}
	obs := []AcceptanceObservation{
		{Item: 0, ItemText: "windows installs clean", Class: "locally_verified", Ref: "ws_win"},
		{Item: 1, ItemText: "the dmg is stapled", Class: "worker_report_only", Ref: "ws_dmg"},
	}
	prog := DeriveContractProgress(c, obs)
	if !prog.HasCriteria {
		t.Fatal("expected criteria")
	}
	if prog.Items[0].State != AcceptanceVerified {
		t.Fatalf("item0 = %q, want verified", prog.Items[0].State)
	}
	if prog.Items[1].State != AcceptanceSupported {
		t.Fatalf("item1 = %q, want supported", prog.Items[1].State)
	}
	if prog.Items[2].State != AcceptanceOpen {
		t.Fatalf("item2 = %q, want open", prog.Items[2].State)
	}
	if prog.NextIndex != 1 {
		t.Fatalf("next = %d, want 1 (first not verified/waived — supported now needs action)", prog.NextIndex)
	}
	if prog.ClosureAllowed {
		t.Fatal("close must be blocked with an open item")
	}
	if !strings.Contains(prog.ClosureReason, "item 2") {
		t.Fatalf("closure reason must name the blocking item: %q", prog.ClosureReason)
	}
	if prog.Counts[AcceptanceVerified] != 1 || prog.Counts[AcceptanceSupported] != 1 || prog.Counts[AcceptanceOpen] != 1 {
		t.Fatalf("counts wrong: %+v", prog.Counts)
	}
}

func TestWorkerReportSupportsNeverVerifies(t *testing.T) {
	c := Contract{Acceptance: []string{"the check passes"}}
	prog := DeriveContractProgress(c, []AcceptanceObservation{
		{Item: 0, ItemText: "the check passes", Class: "worker_report_only", Ref: "ws_x"},
	})
	if prog.Items[0].State == AcceptanceVerified {
		t.Fatal("worker-only evidence must not verify an item")
	}
	if prog.Items[0].State != AcceptanceSupported {
		t.Fatalf("want supported, got %q", prog.Items[0].State)
	}
}

func TestStaleReferenceReopensAndIsNamed(t *testing.T) {
	c := Contract{Acceptance: []string{"the NEW acceptance wording"}}
	// .
	prog := DeriveContractProgress(c, []AcceptanceObservation{
		{Item: 0, ItemText: "the old acceptance wording", Class: "locally_verified", Ref: "ws_x"},
	})
	if prog.Items[0].State != AcceptanceOpen {
		t.Fatalf("a stale reference must leave the item open, got %q", prog.Items[0].State)
	}
	if !prog.Items[0].Stale {
		t.Fatal("the item must be marked stale")
	}
	card := RenderProjectCard("P", "", c, prog)
	if !strings.Contains(strings.ToLower(card), "stale") {
		t.Fatalf("card must name the stale reference: %q", card)
	}
}

func TestNoCriteriaIsNotComplete(t *testing.T) {
	c := Contract{Outcome: "informal exploration"}
	prog := DeriveContractProgress(c, nil)
	if prog.HasCriteria {
		t.Fatal("no acceptance items => no criteria")
	}
	if !prog.ClosureAllowed {
		t.Fatal("an informal project with no contract may close")
	}
	card := RenderProjectCard("P", "", c, prog)
	if !strings.Contains(card, "no acceptance criteria") {
		t.Fatalf("card must say no acceptance criteria, not 100%%: %q", card)
	}
}

func TestManagerCloseGate(t *testing.T) {
	m := NewManager(t.TempDir())
	p, err := m.Create("Beta", "the beta project", "identity", nil, &Contract{
		Outcome:    "ship it",
		Acceptance: []string{"windows installs", "dmg stapled"},
	}, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := m.SetState(p.ID, "closed"); err == nil {
		t.Fatal("close must be refused while items are open")
	}
	if _, err := m.RecordObservation(p.ID, 0, "locally_verified", "ws_win", "ran installer", "ivy"); err != nil {
		t.Fatalf("record item0: %v", err)
	}
	if _, err := m.SetState(p.ID, "closed"); err == nil {
		t.Fatal("close must still be refused with one open item")
	}
	if _, err := m.RecordObservation(p.ID, 1, "host_receipted", "notarization-ticket", "stapled", "ivy"); err != nil {
		t.Fatalf("record item1: %v", err)
	}
	if _, err := m.SetState(p.ID, "closed"); err != nil {
		t.Fatalf("close should now succeed: %v", err)
	}
}

func TestManagerVerifiedNeedsRef(t *testing.T) {
	m := NewManager(t.TempDir())
	p, _ := m.Create("V", "d", "identity", nil, &Contract{Acceptance: []string{"the check"}}, nil)
	if _, err := m.RecordObservation(p.ID, 0, "locally_verified", "", "no ref", "ivy"); err == nil {
		t.Fatal("verified class without a ref must be refused")
	}
	if _, err := m.RecordObservation(p.ID, 0, "locally_verified", "ws_ref", "with ref", "ivy"); err != nil {
		t.Fatalf("verified with ref: %v", err)
	}
	prog, _ := m.Progress(p.ID)
	if prog.Items[0].State != AcceptanceVerified {
		t.Fatalf("want verified, got %q", prog.Items[0].State)
	}
}

func TestManagerWaiverAllowsCloseAndIsAttributable(t *testing.T) {
	m := NewManager(t.TempDir())
	p, _ := m.Create("W", "d", "identity", nil, &Contract{Acceptance: []string{"out of scope now"}}, nil)
	if _, err := m.WaiveItem(p.ID, 0, "ivy", ""); err == nil {
		t.Fatal("a waiver needs a reason")
	}
	if _, err := m.WaiveItem(p.ID, 0, "ivy", "deferred to the next cycle by the operator"); err != nil {
		t.Fatalf("waive: %v", err)
	}
	prog, _ := m.Progress(p.ID)
	if prog.Items[0].State != AcceptanceWaived {
		t.Fatalf("want waived, got %q", prog.Items[0].State)
	}
	if prog.Items[0].Note == "" {
		t.Fatal("the waiver reason must be visible in the derived view")
	}
	if _, err := m.SetState(p.ID, "closed"); err != nil {
		t.Fatalf("a fully-waived contract should close: %v", err)
	}
}

func TestRestartPreservesObservations(t *testing.T) {
	root := t.TempDir()
	m1 := NewManager(root)
	p, _ := m1.Create("R", "d", "identity", nil, &Contract{Acceptance: []string{"the check"}}, nil)
	if _, err := m1.RecordObservation(p.ID, 0, "locally_verified", "ws_x", "checked", "ivy"); err != nil {
		t.Fatalf("record: %v", err)
	}
	// .
	m2 := NewManager(root)
	prog, err := m2.Progress(p.ID)
	if err != nil {
		t.Fatalf("progress after restart: %v", err)
	}
	if prog.Items[0].State != AcceptanceVerified {
		t.Fatalf("observation not preserved across restart: %q", prog.Items[0].State)
	}
}
