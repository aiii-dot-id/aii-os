package project

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestManifestCannotRedirectDir pins the security property that
// DESIGN-PROJECT-WORKSPACE §6 leans on: the workspace lists files with
// os.ReadDir(p.Dir), and p.Dir must be DERIVED from the manager's root
// plus the id — never authored by project.json.
//
// Today the property holds by construction: loadLocked assigns
// filepath.Join(m.root, id) and json.Unmarshal only ever fills the
// Manifest. That is exactly the kind of guarantee a refactor erases
// silently — one line in loadLocked honouring a manifest-supplied path
// and a poisoned project.json starts redirecting a read path the
// operator's browser renders. Construction is a claim until a test
// executes it; the negative control for this test is that one line.
func TestManifestCannotRedirectDir(t *testing.T) {
	root := filepath.Join(t.TempDir(), "projects")
	m := NewManager(root)

	p, err := m.Create("Redirect Probe", "", "operator", nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	honestDir := p.Dir

	// A directory the manifest will try to point at, holding a marker
	// file that must never appear in this project's listing.
	elsewhere := filepath.Join(t.TempDir(), "elsewhere")
	if err := os.MkdirAll(elsewhere, 0o755); err != nil {
		t.Fatalf("elsewhere: %v", err)
	}

	// Poison project.json with every plausible spelling of a directory
	// override, alongside the legitimate fields.
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

	// The listing the workspace performs must still read the project's
	// own directory.
	if _, err := os.ReadDir(got.Dir); err != nil {
		t.Fatalf("read derived dir: %v", err)
	}
	if filepath.Dir(got.Dir) != root {
		t.Fatalf("derived dir escaped the projects root: %q not under %q", got.Dir, root)
	}
}
