package app

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/project"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

func TestEveryWorkItemCarriesWhatTheSessionRecords(t *testing.T) {
	const (
		next     = "read the <b>second</b> log & compare"
		expected = "the counts match to the row"
		falsify  = "a row in one log only"
		readback = "ran wc -l on both: 4120 and 4120"
	)
	a := liveApp(t)
	a.projects = project.NewManager(filepath.Join(t.TempDir(), "projects"))
	p, err := a.projects.Create("evidence", "the recorded fields", "operator", nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.store.SetActiveProject(p.ID); err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	plan := func(id string) {
		t.Helper()
		n, e, f := next, expected, falsify
		if err := a.store.UpdateWorkPlan(ctx, id, nil, &n, nil, &e, &f, nil); err != nil {
			t.Fatal(err)
		}
	}
	// A running child, a delivered child, and a plain session with none.
	queueChild(t, a.store, "ws_live")
	if err := a.store.StartWorkSession(ctx, "ws_live", store.SubagentDescription("compare the logs")); err != nil {
		t.Fatal(err)
	}
	plan("ws_live")
	queueChild(t, a.store, "ws_done")
	if err := a.store.StartWorkSession(ctx, "ws_done", store.SubagentDescription("count the rows")); err != nil {
		t.Fatal(err)
	}
	plan("ws_done")
	if err := a.store.DeliverWorkSession(ctx, "ws_done", "served: they match", store.EvidenceLocallyVerified, readback); err != nil {
		t.Fatal(err)
	}
	queueChild(t, a.store, "ws_bare")
	if err := a.store.StartWorkSession(ctx, "ws_bare", store.SubagentDescription("say hello")); err != nil {
		t.Fatal(err)
	}
	if err := a.store.DeliverWorkSession(ctx, "ws_bare", "served: hello", "", ""); err != nil {
		t.Fatal(err)
	}
	// The boundary gives a delivery a class when none is named; "none"
	// here is the five fields unset, as a session from before H4 holds.
	if _, err := a.store.DB().Exec(`UPDATE work_sessions SET evidence = '' WHERE id = 'ws_bare'`); err != nil {
		t.Fatal(err)
	}

	w, err := a.workQueueState()
	if err != nil {
		t.Fatal(err)
	}
	ws, err := a.getProjectWorkspace(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	find := func(items []dashboard.WorkSessionItem, id string) dashboard.WorkSessionItem {
		t.Helper()
		for _, it := range items {
			if it.ID == id {
				return it
			}
		}
		t.Fatalf("no item %s among %+v", id, items)
		return dashboard.WorkSessionItem{}
	}
	planned := func(path string, it dashboard.WorkSessionItem) {
		t.Helper()
		if it.NextMove != next || it.ExpectedEvidence != expected || it.Falsifier != falsify {
			t.Errorf("%s: the plan is not what the session records: %+v", path, it)
		}
	}
	delivered := func(path string, it dashboard.WorkSessionItem) {
		t.Helper()
		planned(path, it)
		if it.EvidenceClass != store.EvidenceLocallyVerified || it.EvidenceReadback != readback {
			t.Errorf("%s: the evidence is not what the session records: %+v", path, it)
		}
	}
	bare := func(path string, it dashboard.WorkSessionItem) {
		t.Helper()
		js, _ := json.Marshal(it)
		for _, key := range []string{"next_move", "expected_evidence", "falsifier", "evidence_class", "evidence_readback"} {
			if strings.Contains(string(js), `"`+key+`"`) {
				t.Errorf("%s: a session with none carries %s: %s", path, key, js)
			}
		}
	}

	t.Run("home live", func(t *testing.T) { planned("home live", find(w.Live, "ws_live")) })
	t.Run("home delivered", func(t *testing.T) {
		delivered("home delivered", find(w.Delivered, "ws_done"))
		bare("home delivered", find(w.Delivered, "ws_bare"))
	})
	t.Run("project work", func(t *testing.T) {
		planned("project work", find(ws.Work, "ws_live"))
		delivered("project work", find(ws.Work, "ws_done"))
		bare("project work", find(ws.Work, "ws_bare"))
	})
}
