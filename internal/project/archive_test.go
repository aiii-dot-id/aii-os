package project

import (
	"os"
	"path/filepath"
	"testing"
)

// .
// .
// .
// .
func TestArchiveIsRetirementAndDeleteIsItsSecondStep(t *testing.T) {
	m := NewManager(t.TempDir())
	p, err := m.Create("Old idea", "", "operator", nil, &Contract{Outcome: "ship it", Acceptance: []string{"it works"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.SetState(p.ID, "closed"); err == nil {
		t.Fatal("an unmet contract refuses close")
	}
	if _, err := m.SetState(p.ID, "archived"); err != nil {
		t.Fatalf("retiring needs no contract: %v", err)
	}
	live, err := m.Create("Live", "", "operator", nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Delete(live.ID); err == nil {
		t.Fatal("deleting an open project is refused: archive is the first step")
	}
	ps, err := m.List()
	if err != nil || len(ps) != 2 || ps[0].ID != live.ID || ps[1].State != "archived" {
		t.Fatalf("open lists first, archived last: %v %+v", err, ps)
	}
	if _, err := m.SetState(p.ID, "closed"); err == nil {
		t.Fatal("an archived project closes only through open")
	}
	if _, err := m.SetState(p.ID, "open"); err != nil {
		t.Fatalf("unarchive: %v", err)
	}
	if _, err := m.SetState(p.ID, "archived"); err != nil {
		t.Fatal(err)
	}
	dest, err := m.Delete(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dest, manifestName)); err != nil {
		t.Fatalf("the directory is kept in the trash: %v", err)
	}
	if _, err := m.Load(p.ID); err == nil {
		t.Fatal("a deleted project is gone from the root")
	}
	if ps, err := m.List(); err != nil || len(ps) != 1 {
		t.Fatalf("the trash is not a project: %v %d", err, len(ps))
	}
}
