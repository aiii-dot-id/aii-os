package app

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/project"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// .
// .
// .
// .
// .
func TestWorkspaceFilesCapDeclaresRemainder(t *testing.T) {
	dir := t.TempDir()
	a := firstbootApp(dir)
	// .
	// .
	// .
	a.projects = project.NewManager(filepath.Join(dir, "projects"))
	st, err := store.NewMemory()
	if err != nil {
		t.Fatal(err)
	}
	a.store = st
	p, err := a.projects.Create("cap-check", "cap  test", "operator", nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	// .
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
	// .
	// .
	if ws.FilesTotal != 503 {
		t.Fatalf("total: got %d, want 503 — the whole truth must travel with the cap", ws.FilesTotal)
	}
	if !ws.FilesCapped {
		t.Fatal("capped flag missing — the client cannot declare what it was not told")
	}
}
