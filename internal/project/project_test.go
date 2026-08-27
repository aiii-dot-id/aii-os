package project

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProjectLifecycle(t *testing.T) {
	root := filepath.Join(t.TempDir(), "projects")
	m := NewManager(root)

	// Empty root: no projects, no error.
	if ps, err := m.List(); err != nil || len(ps) != 0 {
		t.Fatalf("empty list: %v %d", err, len(ps))
	}

	p, err := m.Create("Business Prospects Tracker", "spreadsheet of prospects and contacts", "operator", nil)
	if err != nil {
		t.Fatal(err)
	}
	if p.ID != "business-prospects-tracker" || p.State != "open" {
		t.Fatalf("unexpected project: %+v", p)
	}

	// Name collision gets a suffix, never an error.
	p2, err := m.Create("Business Prospects Tracker", "", "identity", nil)
	if err != nil {
		t.Fatal(err)
	}
	if p2.ID == p.ID {
		t.Fatal("collision must produce a distinct directory")
	}

	// Update: focus seeds RING4 on selection; attributes replace whole.
	if _, err := m.Update(p.ID, "", "", "drafting column layout with James", map[string]interface{}{"kind": "spreadsheet"}); err != nil {
		t.Fatal(err)
	}
	got, err := m.Load(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Focus != "drafting column layout with James" || got.Attributes["kind"] != "spreadsheet" {
		t.Fatalf("update did not persist: %+v", got.Manifest)
	}

	// Close is durable, not destructive; reopen works.
	if _, err := m.SetState(p2.ID, "closed"); err != nil {
		t.Fatal(err)
	}
	ps, err := m.List()
	if err != nil || len(ps) != 2 {
		t.Fatalf("list: %v %d", err, len(ps))
	}
	if ps[0].State != "open" || ps[1].State != "closed" {
		t.Fatal("open projects must sort first")
	}
	if _, err := m.SetState(p2.ID, "open"); err != nil {
		t.Fatal(err)
	}

	// The collection is files: the directory is the project.
	if err := os.WriteFile(filepath.Join(got.Dir, "notes.md"), []byte("# prospects"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Traversal-shaped IDs never escape the root.
	if _, err := m.Load("../" + p.ID); err == nil {
		// Base()-normalized: resolves to the same project, which is fine —
		// what must never happen is reaching outside the root.
		if !strings.HasPrefix(got.Dir, root) {
			t.Fatal("project dir escaped the root")
		}
	}
	if _, err := m.Load(".."); err == nil {
		t.Fatal("'..' must not load")
	}
}

// D74: a repeated close appended a duplicate "closed" lineage fact per
// call — SetState accepted the non-transition and rewrote the manifest
// each time. The Manager now refuses a no-op state set outright, so
// lineage-on-close (the app adapter) fires once per real open→closed
// transition by construction, for every caller.
func TestSetStateRefusesANonTransition(t *testing.T) {
	m := NewManager(filepath.Join(t.TempDir(), "projects"))
	p, err := m.Create("Twice Closed", "", "operator", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.SetState(p.ID, "closed"); err != nil {
		t.Fatalf("first close: %v", err)
	}
	if _, err := m.SetState(p.ID, "closed"); err == nil {
		t.Fatal("a second close of a closed project succeeded — the duplicate-lineage door (D74)")
	}
	if _, err := m.SetState(p.ID, "open"); err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if _, err := m.SetState(p.ID, "open"); err == nil {
		t.Fatal("a second reopen of an open project succeeded")
	}
}
