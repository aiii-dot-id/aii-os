package app

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/project"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

func TestWorkspaceWorkCapDeclaresRemainder(t *testing.T) {
	dir := t.TempDir()
	a := firstbootApp(t, dir)
	// New()'s full wiring runs in Start; the workspace projection needs
	// the projects manager over a real root (app.go:1129 shape) and a
	// store for attribution + the active-project id.
	a.projects = project.NewManager(filepath.Join(dir, "projects"))
	st, err := store.NewMemory()
	if err != nil {
		t.Fatal(err)
	}
	a.store = st
	p, err := a.projects.Create("cap-work", "cap §8.5 work test", "operator", nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetActiveProject(p.ID); err != nil {
		t.Fatal(err)
	}
	// 22 attributed sessions: two past the cap.
	for i := 0; i < 22; i++ {
		id := fmt.Sprintf("w%02d", i)
		if err := st.StartWorkSession(t.Context(), id, "task "+id); err != nil {
			t.Fatal(err)
		}
		if err := st.DeliverWorkSession(t.Context(), id, "served: done "+id, "", ""); err != nil {
			t.Fatal(err)
		}
	}
	ws, err := a.getProjectWorkspace(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(ws.Work) != 20 {
		t.Fatalf("cap: got %d sessions, want 20", len(ws.Work))
	}
	if !ws.WorkCapped {
		t.Fatal("work_capped missing — the client cannot declare what it was not told")
	}
}
