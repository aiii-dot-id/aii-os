package project

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// .
// .
// .
func mkdirAnswering(answers map[string]error) (func(string) error, *[]string) {
	var tried []string
	return func(p string) error {
		tried = append(tried, filepath.Base(p))
		if err, ok := answers[filepath.Base(p)]; ok {
			return err
		}
		return &os.PathError{Op: "mkdir", Path: p, Err: syscall.ENOTDIR}
	}, &tried
}

// .
// .
// .
// .
func TestClaimDirRefusesAnUnwritableRootInsteadOfSpinning(t *testing.T) {
	mk, _ := mkdirAnswering(nil)
	done := make(chan error, 1)
	go func() {
		_, err := claimDir("/r", "beta", mk)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "claim project directory") {
			t.Fatalf("want a refusal naming the root, got %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("claimDir is spinning on a non-EEXIST mkdir error — the livelock")
	}
}

// .
// .
// .
func TestClaimDirTakesTheFirstFreeNameByCreatingIt(t *testing.T) {
	mk, tried := mkdirAnswering(map[string]error{
		"beta":   os.ErrExist,
		"beta-2": os.ErrExist,
		"beta-3": nil,
	})
	dir, err := claimDir("/r", "beta", mk)
	if err != nil || filepath.Base(dir) != "beta-3" {
		t.Fatalf("got %q %v, want beta-3", dir, err)
	}
	if len(*tried) != 3 {
		t.Fatalf("expected three exclusive attempts, got %v", *tried)
	}
}

func TestClaimDirStopsAtTheBound(t *testing.T) {
	// .
	// .
	mk := func(string) error { return os.ErrExist }
	if _, err := claimDir("/r", "beta", mk); err == nil || !strings.Contains(err.Error(), "already carry") {
		t.Fatalf("an endless run of taken names must stop at the bound, got %v", err)
	}
}

// .
func TestUndoCreateRemovesAndReports(t *testing.T) {
	root := t.TempDir()
	m := &Manager{root: root}
	dir := filepath.Join(root, "ghost")
	if err := os.MkdirAll(filepath.Join(dir, "deep"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := m.undoCreate(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("ghost directory survived the undo: %v", err)
	}
}

// .
// .
// .
// .
// .
func TestCreateDoesNotAdoptADirectoryItDidNotCreate(t *testing.T) {
	m := NewManager(t.TempDir())
	slug := slugify("Beta Tracker")
	foreign := filepath.Join(m.root, slug)
	if err := os.MkdirAll(foreign, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(foreign, "not-ours"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	proj, err := m.Create("Beta Tracker", "", "operator", nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(proj.Dir) == slug {
		t.Fatal("Create adopted the racing actor's directory instead of claiming a fresh one")
	}
	if _, err := os.Stat(filepath.Join(foreign, "not-ours")); err != nil {
		t.Fatalf("the foreign directory was disturbed: %v", err)
	}
}

// .
// .
// .
func TestANewProjectsRootSyncsItsParent(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "projects")
	var synced []string
	rec := func(p string) error { synced = append(synced, p); return nil }
	if err := ensureDurableDir(root, rec); err != nil {
		t.Fatal(err)
	}
	if len(synced) != 1 || synced[0] != parent {
		t.Fatalf("a newly created root must sync its parent %q exactly once, got %v", parent, synced)
	}
	synced = nil
	if err := ensureDurableDir(root, rec); err != nil {
		t.Fatal(err)
	}
	if len(synced) != 0 {
		t.Fatalf("an existing root must not sync anything, got %v", synced)
	}
}
