package app

import (
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/pluginfacility"
)

// .
// .
// .
// .
// .
// .

func retiringRow(a *App, id string) *dashboard.PluginPendingView {
	a.applyFacilitySnapshot()
	for _, v := range a.pluginPendingViews() {
		if v.ID == id {
			v := v
			return &v
		}
	}
	return nil
}

func TestAnUninstallWhoseChildHasNotGoneSaysSoAndOffersNoRetry(t *testing.T) {
	const id = "org.example.audit"
	a, f, rt := auditDiscoveryApp(t, "T0", false)
	f.Observe([]pluginfacility.Observed{{ID: id, Package: "good", Hash: "hash:good"}}, pluginfacility.Policy{Revision: 1})
	auditDiscoveryWait(t, "active", func() bool {
		s := f.Snapshot()
		return len(s.Instances) == 1 && s.Instances[0].State == pluginfacility.StateActive
	})
	rt.pending.Store(true)
	f.Observe(nil, pluginfacility.Policy{Revision: 1})
	auditDiscoveryWait(t, "draining", func() bool {
		s := f.Snapshot()
		return len(s.Instances) == 1 && s.Instances[0].State == pluginfacility.StateDraining && len(s.Instances[0].Residue) > 0
	})
	row := retiringRow(a, id)
	if row == nil || row.Phase != lifeRetiring {
		t.Fatalf("no retiring row: %+v", row)
	}
	if row.Refusal != nil || row.RetryAt != "" {
		t.Errorf("an uninstall is not a refusal and promises no retry: %+v", row)
	}
	if len(row.Residue) == 0 || row.Lifecycle == nil {
		t.Fatalf("the row does not say what is still held or by which generation: %+v", row)
	}
	retiring := 0
	for _, act := range row.Lifecycle.Activations {
		if act.Role == string(pluginfacility.RoleRetiring) && act.Gen != 0 {
			retiring++
		}
	}
	if retiring != 1 {
		t.Errorf("the row names %d retiring generations, want the one", retiring)
	}
	for _, promise := range []string{"download", "activat", "verifying"} {
		if strings.Contains(strings.ToLower(row.Summary), promise) {
			t.Errorf("a stopping plugin is described as coming up: %q", row.Summary)
		}
	}
	if len(a.pluginViews(a.cfg)) != 0 {
		t.Error("it is still listed as installed and serving")
	}
	rt.pending.Store(false)
	f.Settle(id)
	auditDiscoveryWait(t, "the retirement to establish", func() bool { return len(f.Snapshot().Instances) == 0 })
	if row := retiringRow(a, id); row != nil {
		t.Errorf("the row outlived the retirement: %+v", row)
	}
}

func TestAFailedFirstStartIsStoppingUntilItsCleanupIsDoneAndRefusedAfter(t *testing.T) {
	const id = "org.example.audit"
	a, f, rt := auditDiscoveryApp(t, "T0", false)
	rt.pending.Store(true)
	f.Observe([]pluginfacility.Observed{{ID: id, Package: "bad", Hash: "hash:bad"}}, pluginfacility.Policy{Revision: 1})
	auditDiscoveryWait(t, "the failed start to be draining", func() bool {
		s := f.Snapshot()
		return len(s.Instances) == 1 && s.Instances[0].State == pluginfacility.StateDraining
	})
	row := retiringRow(a, id)
	if row == nil || row.Phase != lifeRetiring {
		t.Fatalf("no retiring row: %+v", row)
	}
	// .
	if row.Lifecycle == nil || row.Lifecycle.Refusal == nil || !strings.Contains(row.Lifecycle.Refusal.Cause, "health failed") {
		t.Errorf("the row does not carry why the start failed: %+v", row.Lifecycle)
	}
	if starts := rt.starts.Load(); starts != 1 {
		t.Errorf("%d engines were started beside one that has not gone", starts)
	}
	rt.pending.Store(false)
	f.Settle(id)
	auditDiscoveryWait(t, "the cleanup to finish and the refusal to stand alone", func() bool {
		r := retiringRow(a, id)
		return r != nil && r.Phase == lifeRefused
	})
}

func TestAPredecessorRetiringBehindWhatServesIsOnTheServingCard(t *testing.T) {
	const id = "org.example.audit"
	a, f, rt := auditDiscoveryApp(t, "T0", false)
	f.Observe([]pluginfacility.Observed{{ID: id, Package: "good", Hash: "hash:good"}}, pluginfacility.Policy{Revision: 1})
	auditDiscoveryWait(t, "active", func() bool {
		s := f.Snapshot()
		return len(s.Instances) == 1 && s.Instances[0].State == pluginfacility.StateActive
	})
	rt.pending.Store(true)
	f.Observe([]pluginfacility.Observed{{ID: id, Package: "good2", Hash: "hash:good2"}}, pluginfacility.Policy{Revision: 1})
	auditDiscoveryWait(t, "the successor to serve with its predecessor still retiring", func() bool {
		s := f.Snapshot()
		return len(s.Instances) == 1 && s.Instances[0].State == pluginfacility.StateActive && len(s.Instances[0].Residue) > 0
	})
	a.applyFacilitySnapshot()
	views := a.pluginViews(a.cfg)
	if len(views) != 1 || views[0].Version != "good2" || views[0].Lifecycle == nil {
		t.Fatalf("the serving release is not the installed card: %+v", views)
	}
	roles := map[string]int{}
	for _, act := range views[0].Lifecycle.Activations {
		roles[act.Role]++
	}
	if roles["active"] != 1 || roles["retiring"] != 1 || len(views[0].Lifecycle.Residue) == 0 {
		t.Errorf("the card does not show what serves beside what is still retiring: %+v", views[0].Lifecycle)
	}
	if row := retiringRow(a, id); row != nil {
		t.Errorf("a plugin that serves also has a pending row: %+v", row)
	}
	rt.pending.Store(false)
	f.Settle(id)
	auditDiscoveryWait(t, "the predecessor's retirement to establish", func() bool {
		s := f.Snapshot()
		return len(s.Instances) == 1 && len(s.Instances[0].Residue) == 0 && len(s.Instances[0].Activations) == 1
	})
}
