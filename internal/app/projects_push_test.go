package app

import (
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
)

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
func TestIdentityProjectMutationBroadcasts(t *testing.T) {
	dir := t.TempDir()
	keyPath, ledgerPath, dbPath := birthFixture(t, dir, "ProjBcast")
	cfg := safebootConfig(t, dir, "ProjBcast", keyPath, ledgerPath, dbPath)
	cfg.Dashboard.Port = 0
	a := New(cfg)
	if err := startLiveForTest(a); err != nil {
		t.Fatalf("startLive: %v", err)
	}
	t.Cleanup(a.Stop)

	// .
	port := projectsAdapter{a}
	seed1, err := port.Create("alpha", "first project", nil, nil, nil)
	if err != nil {
		t.Fatalf("create alpha: %v", err)
	}
	if _, err := port.Create("beta", "second project", nil, nil, nil); err != nil {
		t.Fatalf("create beta: %v", err)
	}

	addr := a.dashboard.Origin()
	conn := dialWSS(t, addr)
	t.Cleanup(func() { conn.CloseNow() })

	// .
	// .
	// .
	sendQuery(t, conn, "projects")
	base := readUntil(t, conn, isProjectsMsg)
	if len(base.Projects) != 2 {
		t.Fatalf("baseline dock: got %d projects, want 2", len(base.Projects))
	}
	for _, p := range base.Projects {
		if p.Active {
			t.Fatalf("baseline: project %s active before any select", p.ID)
		}
	}

	// .
	// .
	// .
	if _, err := port.Select(seed1.ID); err != nil {
		t.Fatalf("identity select: %v", err)
	}

	// .
	// .
	m := readUntil(t, conn, isProjectsMsg)
	if m.RequestID != "" {
		t.Fatalf("identity-mutation push carried a request id %q — answers do; broadcasts must not", m.RequestID)
	}
	var alpha *dashboard.ProjectState
	for i := range m.Projects {
		if m.Projects[i].ID == seed1.ID {
			alpha = &m.Projects[i]
		}
	}
	if alpha == nil {
		t.Fatalf("pushed dock has no %s: %+v", seed1.ID, m.Projects)
	}
	if !alpha.Active {
		t.Fatal("pushed dock: alpha not marked active — identity select did not reach the wire")
	}

	// .
	// .
	// .
	// .
	fresh, err := port.Create("gamma", "created while watched", nil, nil, nil)
	if err != nil {
		t.Fatalf("create gamma: %v", err)
	}
	m = readUntil(t, conn, isProjectsMsg)
	if !hasProject(m, fresh.ID) {
		t.Fatalf("identity create did not push: %s missing from dock", fresh.ID)
	}

	if _, err := port.SetState(fresh.ID, "closed"); err != nil {
		t.Fatalf("close gamma: %v", err)
	}
	m = readUntil(t, conn, isProjectsMsg)
	found := false
	for _, p := range m.Projects {
		if p.ID == fresh.ID {
			found = true
			if p.State != "closed" {
				t.Fatalf("pushed dock: gamma state %q, want closed", p.State)
			}
		}
	}
	if !found {
		t.Fatalf("close did not push: %s missing from dock", fresh.ID)
	}

	// .
	// .
	// .
	if _, err := port.SetState(seed1.ID, "closed"); err != nil {
		t.Fatalf("close focused alpha: %v", err)
	}
	m = readUntil(t, conn, isProjectsMsg)
	for _, p := range m.Projects {
		if p.Active {
			t.Fatalf("pushed dock: %s still active after the focused project closed", p.ID)
		}
	}

	// .
	// .
	// .
	turns, err := a.store.RecentTurnsIncludingSystem(10)
	if err != nil {
		t.Fatalf("read transcript: %v", err)
	}
	recorded := false
	for _, tn := range turns {
		if strings.Contains(tn.Content, seed1.ID) {
			recorded = true
		}
	}
	if !recorded {
		t.Fatalf("select transition not recorded in transcript")
	}
}

func isProjectsMsg(m *dashboard.ServerMessage) bool { return m.Type == "projects" }

func hasProject(m *dashboard.ServerMessage, id string) bool {
	for _, p := range m.Projects {
		if p.ID == id {
			return true
		}
	}
	return false
}

// .
// .
// .
func TestIdentityProjectMutationBroadcastIsSilentWithNoConnections(t *testing.T) {
	// .
	// .
	a, _ := focusFixture(t)
	port := projectsAdapter{a}
	p, err := port.Create("quiet", "nobody is watching", nil, nil, nil)
	if err != nil {
		t.Fatalf("create with no dashboard must succeed: %v", err)
	}
	if _, err := port.Select(p.ID); err != nil {
		t.Fatalf("select with no dashboard must succeed: %v", err)
	}
	if _, err := port.SetState(p.ID, "closed"); err != nil {
		t.Fatalf("close with no dashboard must succeed: %v", err)
	}
}
