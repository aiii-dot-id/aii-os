package project

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
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
func TestManifestCannotRedirectDir(t *testing.T) {
	root := filepath.Join(t.TempDir(), "projects")
	m := NewManager(root)

	p, err := m.Create("Redirect Probe", "", "operator", nil, nil, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	honestDir := p.Dir

	// .
	// .
	elsewhere := filepath.Join(t.TempDir(), "elsewhere")
	if err := os.MkdirAll(elsewhere, 0o755); err != nil {
		t.Fatalf("elsewhere: %v", err)
	}

	// .
	// .
	raw := map[string]interface{}{
		"name":       p.Name,
		"state":      p.State,
		"created_by": p.CreatedBy,
		"created_at": p.CreatedAt,
		"updated_at": p.UpdatedAt,
		"dir":        elsewhere,
		"Dir":        elsewhere,
		"path":       elsewhere,
		"root":       elsewhere,
	}
	b, err := json.Marshal(raw)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(honestDir, "project.json"), b, 0o644); err != nil {
		t.Fatalf("write poisoned manifest: %v", err)
	}

	got, err := m.Load(p.ID)
	if err != nil {
		t.Fatalf("load after poisoning: %v", err)
	}
	if got.Dir != honestDir {
		t.Fatalf("manifest redirected Dir: got %q, want %q (derived from root+id)", got.Dir, honestDir)
	}
	if got.Dir == elsewhere {
		t.Fatal("manifest redirected Dir to an attacker-chosen path")
	}

	// .
	// .
	if _, err := os.ReadDir(got.Dir); err != nil {
		t.Fatalf("read derived dir: %v", err)
	}
	if filepath.Dir(got.Dir) != root {
		t.Fatalf("derived dir escaped the projects root: %q not under %q", got.Dir, root)
	}
}
