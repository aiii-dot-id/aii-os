package app

import (
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/project"
)

// .
// .
// .
// .
func TestDashboardProgressMatchesDerivation(t *testing.T) {
	mgr := project.NewManager(t.TempDir())
	p, err := mgr.Create("Beta", "d", "identity", nil, &project.Contract{
		Outcome:    "ship it",
		Acceptance: []string{"windows installs", "dmg stapled", "deb registers"},
	}, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := mgr.RecordObservationByText(p.ID, "windows installs", "locally_verified", "ws_w", "ran", "ivy"); err != nil {
		t.Fatalf("record verified: %v", err)
	}
	if _, err := mgr.RecordObservationByText(p.ID, "dmg stapled", "worker_report_only", "ws_d", "child said so", "ivy"); err != nil {
		t.Fatalf("record supported: %v", err)
	}
	got, err := mgr.Load(p.ID)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	derived := project.DeriveContractProgress(got.Contract, got.Observations)
	dash := projectDashboardProgress(got)
	if dash == nil || len(dash.Items) != len(derived.Items) {
		t.Fatalf("dashboard progress shape mismatch: %+v", dash)
	}
	for i := range derived.Items {
		if dash.Items[i].State != derived.Items[i].State {
			t.Fatalf("item %d: dashboard %q != derived %q", i, dash.Items[i].State, derived.Items[i].State)
		}
	}
	if dash.Items[0].State != project.AcceptanceVerified {
		t.Fatalf("item0 = %q, want verified", dash.Items[0].State)
	}
	if dash.Items[1].State != project.AcceptanceSupported {
		t.Fatalf("item1 = %q, want supported", dash.Items[1].State)
	}
	if dash.Items[2].State != project.AcceptanceOpen {
		t.Fatalf("item2 = %q, want open", dash.Items[2].State)
	}
	if dash.ClosureAllowed {
		t.Fatal("dashboard must not imply a close the operation would reject")
	}
	if dash.NextIndex != 1 {
		t.Fatalf("next index = %d, want 1 (supported now needs action, before the open item)", dash.NextIndex)
	}
}
