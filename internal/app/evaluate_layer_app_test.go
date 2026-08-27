package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/project"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// evaluate_layer_app_test.go — generic sub-agent framing and the
// mechanical lineage trail a closing project leaves in its directory.

func TestSubagentFrameCarriesOnlyTheGoal(t *testing.T) {
	msg := buildSubagentGoal(2, "survey the store layer")
	for _, want := range []string{
		"[sub-agent, depth 2]",
		"survey the store layer",
	} {
		if !strings.Contains(msg, want) {
			t.Fatalf("spawn frame missing %q:\n%s", want, msg)
		}
	}
	if strings.Contains(msg, "served|partial|unserved") {
		t.Fatalf("generic substrate imposed a self-verdict protocol:\n%s", msg)
	}
}

func TestCompactTextPreservesTheConclusion(t *testing.T) {
	got := compactText("0123456789-CONCLUSION", 12)
	if len([]rune(got)) != 12 || !strings.HasSuffix(got, "ION") {
		t.Fatalf("compact text lost its bounded conclusion: %q", got)
	}
}

func lineageFixture(t *testing.T) (*App, *project.Project) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.New(filepath.Join(dir, "aii.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	a := New(&Config{SourcePath: filepath.Join(dir, "config.json")})
	a.store = st
	a.projects = project.NewManager(filepath.Join(dir, "projects"))
	p, err := a.projects.Create("voice-eval", "the eval corpus work", "identity", nil)
	if err != nil {
		t.Fatal(err)
	}
	return a, p
}

// R62: project truth is files in the project directory. Closing leaves
// one MECHANICAL line — when, and under what focus — so the trail of
// attempts stops being memory-only. The identity enriches lineage.md
// with meaning, or doesn't; the substrate only couriers facts.
func TestClosingAProjectLeavesALineageLine(t *testing.T) {
	a, p := lineageFixture(t)
	if _, err := a.projects.Update(p.ID, "", "", "prove the sidecar verifies", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := (projectsAdapter{a}).SetState(p.ID, "closed"); err != nil {
		t.Fatalf("close: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(p.Dir, "lineage.md"))
	if err != nil {
		t.Fatal("NO LINEAGE LINE — the attempt left no trail")
	}
	line := string(raw)
	if !strings.Contains(line, "closed 20") || !strings.Contains(line, "prove the sidecar verifies") {
		t.Fatalf("lineage line carries neither date nor focus: %q", line)
	}

	// Reopen and close again: the trail APPENDS — history, not a field.
	if _, err := (projectsAdapter{a}).SetState(p.ID, "open"); err != nil {
		t.Fatal(err)
	}
	if _, err := (projectsAdapter{a}).SetState(p.ID, "closed"); err != nil {
		t.Fatal(err)
	}
	raw, _ = os.ReadFile(filepath.Join(p.Dir, "lineage.md"))
	if got := strings.Count(string(raw), "closed 20"); got != 2 {
		t.Fatalf("two closes, %d lineage lines", got)
	}
}

// Opening writes nothing: the trail records ends, not beginnings (the
// manifest already records creation).
func TestOpeningAProjectWritesNoLineage(t *testing.T) {
	a, p := lineageFixture(t)
	if _, err := (projectsAdapter{a}).SetState(p.ID, "closed"); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(p.Dir, "lineage.md"))
	if _, err := (projectsAdapter{a}).SetState(p.ID, "open"); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(filepath.Join(p.Dir, "lineage.md"))
	if string(before) != string(after) {
		t.Fatalf("reopening wrote lineage: %q -> %q", before, after)
	}
}
