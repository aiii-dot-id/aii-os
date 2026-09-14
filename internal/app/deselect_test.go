package app

import (
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/project"
)

// .
// .
// .
// .

func projectApp(t *testing.T) (*App, string) {
	t.Helper()
	a := standingApp(t)
	root := t.TempDir()
	mgr := project.NewManager(root)
	a.projects = mgr
	p, err := mgr.Create("ui-reform", "a durable workroom", "identity", nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	return a, p.ID
}

func TestDeselectDropsFocusWithoutTouchingTheProjectOrTheSession(t *testing.T) {
	a, id := projectApp(t)

	if _, err := a.selectProject(id); err != nil {
		t.Fatal(err)
	}
	// .
	if err := a.store.StartWorkSession("ws_live", "work that outlives the focus"); err != nil {
		t.Fatal(err)
	}
	if p, _ := a.activeOpenProject(); p == nil {
		t.Fatal("select did not take")
	}

	name, err := a.deselectProject()
	if err != nil {
		t.Fatalf("deselect: %v", err)
	}
	if name != "ui-reform" {
		t.Fatalf("deselect reported %q, want the name it dropped", name)
	}

	// .
	if p, why := a.activeOpenProject(); p != nil {
		t.Fatalf("still focused after deselect (%v)", why)
	}
	// .
	pp, err := a.projects.Load(id)
	if err != nil {
		t.Fatalf("deselect damaged the project: %v", err)
	}
	if pp.State != "open" {
		t.Fatalf("deselect closed the project (state=%q) — that is a different intent", pp.State)
	}
	// .
	ws, err := a.store.ActiveWorkSession()
	if err != nil {
		t.Fatal(err)
	}
	if ws == nil || ws.ID != "ws_live" {
		t.Fatalf("deselect disturbed the work session: %+v", ws)
	}
	// .
	state, err := a.buildWorkState()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(state, "Current project") {
		t.Fatalf("the working state still names a project after deselect:\n%s", state)
	}
	// .
	var seen int
	if err := a.store.DB().QueryRow(
		`SELECT COUNT(*) FROM conversations WHERE role='system' AND content LIKE '%now working outside any project%'`).
		Scan(&seen); err != nil {
		t.Fatal(err)
	}
	if seen != 1 {
		t.Fatalf("transition recorded %d times, want exactly 1", seen)
	}
}

// .
// .
func TestDeselectWithNoFocusIsAQuietNoOp(t *testing.T) {
	a, _ := projectApp(t)

	name, err := a.deselectProject()
	if err != nil {
		t.Fatalf("deselect with no focus must not error: %v", err)
	}
	if name != "" {
		t.Fatalf("reported %q dropped when nothing was focused", name)
	}
	var seen int
	if err := a.store.DB().QueryRow(
		`SELECT COUNT(*) FROM conversations WHERE content LIKE '%outside any project%'`).Scan(&seen); err != nil {
		t.Fatal(err)
	}
	if seen != 0 {
		t.Fatalf("a no-op wrote %d transition line(s) — the record must not narrate nothing", seen)
	}
}

// .
// .
// .
// .
// .
// .
func TestFocusTransitionIsAtomicWithItsRecord(t *testing.T) {
	a, id := projectApp(t)
	if _, err := a.selectProject(id); err != nil {
		t.Fatal(err)
	}
	if a.store.ActiveProjectID() != id {
		t.Fatal("setup: focus did not take")
	}

	// .
	if _, err := a.store.DB().Exec(`DROP TABLE conversations`); err != nil {
		t.Fatal(err)
	}

	if _, err := a.deselectProject(); err == nil {
		t.Error("a focus change whose record cannot be written must FAIL, not report success with a log line")
	}
	// .
	if got := a.store.ActiveProjectID(); got != id {
		t.Errorf("focus moved to %q even though the transition could not be recorded — the two halves are not atomic", got)
	}
}

// .
func TestSelectTransitionIsAtomicWithItsRecord(t *testing.T) {
	a, id := projectApp(t)
	if _, err := a.store.DB().Exec(`DROP TABLE conversations`); err != nil {
		t.Fatal(err)
	}
	if _, err := a.selectProject(id); err == nil {
		t.Error("select must fail when its transition cannot be recorded")
	}
	if got := a.store.ActiveProjectID(); got != "" {
		t.Errorf("focus moved to %q despite the failed transaction", got)
	}
}
