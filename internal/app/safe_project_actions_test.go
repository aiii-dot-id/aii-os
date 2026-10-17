package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/identity"
	"github.com/aiii-dot-id/aii-os/internal/project"
)

func TestBootSafeKeepsProjectFilesReadOnlyAndProjectsReadable(t *testing.T) {
	dir := t.TempDir()
	keyPath, ledgerPath, dbPath := birthFixture(t, dir, "SafeProjects")
	buildPriorProjection(t, ledgerPath, dbPath)
	root := filepath.Join(dir, "projects")
	existing, err := project.NewManager(root).Create("Existing", "operator work", "operator", nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(existing.Dir, "project.json")
	before, stamp := fileDigest(t, manifest)
	tamperChain(t, keyPath, ledgerPath)
	cfg := safebootConfig(t, dir, "SafeProjects", keyPath, ledgerPath, dbPath)
	cfg.Projects.Root = root
	a := New(cfg)
	if err := startLiveForTest(a); err != nil {
		t.Fatal(err)
	}
	defer stopApp(t, a)
	if _, safe := a.SafeMode(); !safe {
		t.Fatal("fixture did not enter boot-SAFE")
	}
	for _, action := range []string{"create", "update", "close", "select", "deselect", "evidence", "waive"} {
		args := map[string]any{"action": "project." + action, "project": existing.ID,
			"name": "Another", "item": "an acceptance item", "reason": "not checked"}
		if _, err := a.engine.ExecuteAction(context.Background(), "verb", "work", args); !errors.Is(err, identity.ErrProjectSafe) {
			t.Errorf("project.%s was not refused under boot-SAFE: %v", action, err)
		}
	}
	after, afterStamp := fileDigest(t, manifest)
	if after != before || !afterStamp.Equal(stamp) {
		t.Fatal("a SAFE project act changed the operator's project file")
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 1 {
		t.Fatalf("a SAFE project act created a new directory: %+v %v", entries, err)
	}
	read, err := a.engine.ExecuteAction(context.Background(), "verb", "recall", map[string]any{"source": "projects"})
	if err != nil || !strings.Contains(read, "Existing") {
		t.Fatalf("SAFE lost read-only project context: %q %v", read, err)
	}
}

// Both boots put the projects at projects.root or, unset, at projects/
// under the tools' working directory (wireProjects).
func TestBothBootsWireTheProjectsAtTheirRoot(t *testing.T) {
	for _, boot := range []struct {
		name string
		run  func(a *App) error
	}{
		{"live", startLiveForTest},
		{"safe", func(a *App) error { return a.startSafeBoot(safeRecord, "test: the projects are wired in SAFE too") }},
	} {
		for _, set := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/root set %t", boot.name, set), func(t *testing.T) {
				dir := t.TempDir()
				keyPath, ledgerPath, dbPath := birthFixture(t, dir, "Projects")
				cfg := safebootConfig(t, dir, "Projects", keyPath, ledgerPath, dbPath)
				want := filepath.Join(cfg.Tools.CWD, "projects")
				if set {
					want = filepath.Join(dir, "elsewhere")
					cfg.Projects.Root = want
				}
				a := New(cfg)
				t.Cleanup(func() { stopApp(t, a) })
				if err := boot.run(a); err != nil {
					t.Fatalf("%s boot: %v", boot.name, err)
				}
				if _, safe := a.SafeMode(); safe != (boot.name == "safe") {
					t.Fatalf("the %s boot came up with SAFE %t", boot.name, safe)
				}
				if a.projects == nil || a.projects.Root() != want {
					t.Fatalf("the %s boot wired the projects at %v, not %s", boot.name, a.projects, want)
				}
			})
		}
	}
}
