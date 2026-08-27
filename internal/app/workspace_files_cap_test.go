package app

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/project"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// R18 §9.2: the one-level file listing is capped at 500 entries WITH
// the remainder declared (files_total + files_capped on the wire).
// A cap without the declaration is the dishonest choice the ruling
// names — the operator would read a shortened list as the whole
// directory. This test pins all three facts together.
func TestWorkspaceFilesCapDeclaresRemainder(t *testing.T) {
	dir := t.TempDir()
	a := firstbootApp(dir)
	// New()'s full wiring runs in Start; the workspace projection needs
	// the projects manager over a real root (app.go:1129 shape) and a
	// store for the active-project id.
	a.projects = project.NewManager(filepath.Join(dir, "projects"))
	st, err := store.NewMemory()
	if err != nil {
		t.Fatal(err)
	}
	a.store = st
	p, err := a.projects.Create("cap-check", "cap §9.2 test", "operator", nil)
	if err != nil {
		t.Fatal(err)
	}
	// 502 entries: two past the cap, in the project's own directory.
	for i := 0; i < 502; i++ {
		if err := os.WriteFile(filepath.Join(p.Dir, fmt.Sprintf("f%04d.txt", i)), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ws, err := a.getProjectWorkspace(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(ws.Files) != workspaceFileCap {
		t.Fatalf("cap: got %d files, want %d", len(ws.Files), workspaceFileCap)
	}
	// 502 entries + the project manifest the manager itself writes —
	// the directory's whole truth is what FilesTotal must carry.
	if ws.FilesTotal != 503 {
		t.Fatalf("total: got %d, want 503 — the whole truth must travel with the cap", ws.FilesTotal)
	}
	if !ws.FilesCapped {
		t.Fatal("capped flag missing — the client cannot declare what it was not told")
	}
}
