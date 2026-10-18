package project

import (
	"encoding/json"
	"github.com/aiii-dot-id/aii-os/internal/atomicfile"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The contract is TYPED and durable: what outcome is sought, what would
// confirm it, what bounds it. It used to have no home but the untyped
// attributes bag — which the project tool actively advertised as the
// place for "the outcome you are pursuing, what would prove it done",
// placing contract fields where their spelling could not be checked.
func TestTheContractSurvivesAReload(t *testing.T) {
	m := NewManager(t.TempDir())
	p, err := m.Create("Alpha", "a description", "operator", nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	c := &Contract{
		Outcome:     "the installer works on all three platforms",
		Acceptance:  []string{"a clean Windows box installs and registers", "the dmg is notarized and stapled"},
		Constraints: []string{"no unsigned binaries"},
	}
	if _, err := m.ApplyPatch(p.ID, nil, nil, nil, nil, c, nil); err != nil {
		t.Fatal(err)
	}

	got, err := NewManager(m.root).Load(p.ID) // a fresh manager: read from disk, not memory
	if err != nil {
		t.Fatal(err)
	}
	if got.Contract.Outcome != c.Outcome {
		t.Fatalf("outcome did not survive: %q", got.Contract.Outcome)
	}
	if len(got.Contract.Acceptance) != 2 || got.Contract.Acceptance[1] != c.Acceptance[1] {
		t.Fatalf("acceptance did not survive: %v", got.Contract.Acceptance)
	}
	if len(got.Contract.Constraints) != 1 {
		t.Fatalf("constraints did not survive: %v", got.Contract.Constraints)
	}
	if got.Contract.IsZero() {
		t.Fatal("an authored contract must not report itself unauthored")
	}
}

// A manifest written before the contract existed must still load. The
// fields are additive and omitempty, so absence is absence.
func TestAManifestWithoutAContractStillLoads(t *testing.T) {
	root := t.TempDir()
	m := NewManager(root)
	p, err := m.Create("Legacy", "written before the typed contract", "operator", nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Rewrite the manifest without any contract key at all.
	raw := map[string]any{
		"name": "Legacy", "state": "open", "created_by": "operator",
		"created_at": "2026-08-01T00:00:00Z", "updated_at": "2026-08-01T00:00:00Z",
	}
	b, _ := json.Marshal(raw)
	if err := os.WriteFile(filepath.Join(p.Dir, "project.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := NewManager(root).Load(p.ID)
	if err != nil {
		t.Fatalf("a pre-contract manifest must still load: %v", err)
	}
	if !got.Contract.IsZero() || got.Parent != "" {
		t.Fatal("absence must read as absence, not as an authored empty contract")
	}
}

// Authority-bearing semantics never live in an untyped bag. Two
// spellings of one semantic is the divergence the typed field prevents,
// so the write is refused and the refusal NAMES the field to use.
func TestAuthorityBearingKeysAreRefusedInAttributes(t *testing.T) {
	m := NewManager(t.TempDir())
	p, err := m.Create("Alpha", "", "operator", nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for key, field := range map[string]string{
		"outcome":     "contract.outcome",
		"acceptance":  "contract.acceptance",
		"constraints": "contract.constraints",
		"parent":      "parent",
		"Outcome":     "contract.outcome", // case is not a loophole
	} {
		_, err := m.ApplyPatch(p.ID, nil, nil, nil, nil, nil,
			map[string]any{key: "something"})
		if err == nil {
			t.Fatalf("attribute %q is authority-bearing and must be refused", key)
		}
		if !strings.Contains(err.Error(), field) {
			t.Fatalf("the refusal for %q must name %q, got: %v", key, field, err)
		}
	}
	// An ordinary extension still rides the envelope.
	if _, err := m.ApplyPatch(p.ID, nil, nil, nil, nil, nil,
		map[string]any{"kind": "spreadsheet"}); err != nil {
		t.Fatalf("attributes remain open for extensions: %v", err)
	}
}

// Hierarchy is a parent link on an ordinary project. Every surface that
// renders it walks this chain, so a cycle is a hang, not a curiosity.
func TestParentRefusesSelfCyclesAndPhantoms(t *testing.T) {
	m := NewManager(t.TempDir())
	a, _ := m.Create("A", "", "operator", nil, nil, nil)
	b, _ := m.Create("B", "", "operator", nil, nil, nil)
	c, _ := m.Create("C", "", "operator", nil, nil, nil)

	if _, err := m.ApplyPatch(a.ID, nil, nil, nil, &a.ID, nil, nil); err == nil {
		t.Fatal("a project must not be its own parent")
	}
	ghost := "no-such-project"
	if _, err := m.ApplyPatch(a.ID, nil, nil, nil, &ghost, nil, nil); err == nil {
		t.Fatal("a parent that does not exist must be refused")
	}

	// A -> B -> C, then C -> A would close the loop.
	if _, err := m.ApplyPatch(a.ID, nil, nil, nil, &b.ID, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := m.ApplyPatch(b.ID, nil, nil, nil, &c.ID, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := m.ApplyPatch(c.ID, nil, nil, nil, &a.ID, nil, nil); err == nil {
		t.Fatal("C -> A closes a cycle and must be refused")
	}
	// Clearing is always allowed.
	empty := ""
	if _, err := m.ApplyPatch(a.ID, nil, nil, nil, &empty, nil, nil); err != nil {
		t.Fatalf("clearing a parent must be allowed: %v", err)
	}
}

func TestCreationRefusesAuthorityKeysBeforeTheDirectoryExists(t *testing.T) {
	root := t.TempDir()
	m := NewManager(root)
	for _, key := range []string{"outcome", "acceptance", "constraints", "parent", "OUTCOME"} {
		p, err := m.Create("Alpha", "", "operator", nil, nil, map[string]any{key: "x"})
		if err == nil {
			t.Fatalf("Create must refuse the authority key %q", key)
		}
		if p != nil {
			t.Fatalf("a refused Create must not return a project (%q)", key)
		}
		// And the directory must not survive the refusal.
		if entries, derr := os.ReadDir(root); derr == nil && len(entries) != 0 {
			t.Fatalf("a refused Create left %d directory entries behind (%q)", len(entries), key)
		}
	}
	// The envelope is still open for ordinary extensions.
	if _, err := m.Create("Beta", "", "operator", nil, nil, map[string]any{"kind": "spreadsheet"}); err != nil {
		t.Fatalf("attributes remain open for extensions: %v", err)
	}
}

// Clearing a parent is a real request, distinct from not mentioning one.
func TestUpdateCanClearAParentAndAbsenceLeavesItAlone(t *testing.T) {
	m := NewManager(t.TempDir())
	a, _ := m.Create("A", "", "operator", nil, nil, nil)
	b, _ := m.Create("B", "", "operator", nil, nil, nil)

	set := b.ID
	if _, err := m.Update(a.ID, "", "", "", &set, nil, nil); err != nil {
		t.Fatal(err)
	}
	if got, _ := m.Load(a.ID); got.Parent != b.ID {
		t.Fatalf("parent not set: %q", got.Parent)
	}
	// Absence leaves it alone.
	if _, err := m.Update(a.ID, "", "", "still here", nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if got, _ := m.Load(a.ID); got.Parent != b.ID {
		t.Fatalf("an omitted parent must leave the hierarchy untouched, got %q", got.Parent)
	}
	// A pointer to "" clears it.
	empty := ""
	if _, err := m.Update(a.ID, "", "", "", &empty, nil, nil); err != nil {
		t.Fatal(err)
	}
	if got, _ := m.Load(a.ID); got.Parent != "" {
		t.Fatalf("parent should be cleared, got %q", got.Parent)
	}
}

func TestCreationPersistsTheWholeContract(t *testing.T) {
	root := t.TempDir()
	m := NewManager(root)
	parent, err := m.Create("Umbrella", "", "operator", nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	pid := parent.ID
	created, err := m.Create("Alpha", "a description", "identity", &pid, &Contract{
		Outcome:     "ship the beta on three platforms",
		Acceptance:  []string{"windows installs clean", "the dmg is stapled", "the deb registers"},
		Constraints: []string{"no unsigned binaries", "no network at install time"},
	}, map[string]any{"kind": "release"})
	if err != nil {
		t.Fatal(err)
	}

	// A FRESH manager: read from disk, not from anything held in memory.
	got, err := NewManager(root).Load(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Parent != pid {
		t.Fatalf("parent not persisted at creation: %q", got.Parent)
	}
	if got.Contract.Outcome != "ship the beta on three platforms" {
		t.Fatalf("outcome not persisted at creation: %q", got.Contract.Outcome)
	}
	// ORDER is part of the content: a claim that moves reads as a
	// different claim.
	want := []string{"windows installs clean", "the dmg is stapled", "the deb registers"}
	if len(got.Contract.Acceptance) != len(want) {
		t.Fatalf("acceptance not persisted: %v", got.Contract.Acceptance)
	}
	for i := range want {
		if got.Contract.Acceptance[i] != want[i] {
			t.Fatalf("acceptance[%d] = %q, want %q — authored order must survive", i, got.Contract.Acceptance[i], want[i])
		}
	}
	if len(got.Contract.Constraints) != 2 || got.Contract.Constraints[0] != "no unsigned binaries" {
		t.Fatalf("constraints not persisted in order: %v", got.Contract.Constraints)
	}
	if got.Attributes["kind"] != "release" {
		t.Fatal("the extension envelope must still ride creation")
	}
}

// A phantom parent must leave nothing behind. A half-born project takes
// the name and pushes the next Create onto a "-2" suffix around
// something that never existed.
func TestCreationWithAPhantomParentLeavesNoDirectory(t *testing.T) {
	root := t.TempDir()
	m := NewManager(root)
	ghost := "no-such-project"
	if _, err := m.Create("Alpha", "", "operator", &ghost, nil, nil); err == nil {
		t.Fatal("a parent that does not exist must be refused at creation")
	}
	entries, err := os.ReadDir(root)
	if err == nil && len(entries) != 0 {
		t.Fatalf("a refused creation left %d entries behind", len(entries))
	}
}

func TestACreatedProjectIsDurablyPublished(t *testing.T) {
	root := t.TempDir()
	m := NewManager(root)
	p, err := m.Create("Alpha", "durable", "operator", nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	// The manifest is readable from a fresh manager — the ordinary
	// success this test's failure modes are measured against.
	if got, err := NewManager(root).Load(p.ID); err != nil || got.Name != "Alpha" {
		t.Fatalf("created project must load: %v", err)
	}

	// The publication path is the one that reports durability. A
	// manifest written into a directory that cannot be synced must
	// surface that rather than returning success.
	gone := filepath.Join(root, "vanished")
	if err := os.MkdirAll(gone, 0o755); err != nil {
		t.Fatal(err)
	}
	mf := Manifest{Name: "X", State: "open", CreatedBy: "operator"}
	if err := os.RemoveAll(gone); err != nil {
		t.Fatal(err)
	}
	if err := writeManifest(gone, &mf); err == nil {
		t.Fatal("writing a manifest into a directory that does not exist must fail, not report success")
	}

	// The published-but-not-durable branch is NOT exercised here, and
	// deliberately so. Reaching it needs a replaceable publish function,
	// and test/atomicfile_contract_test.go forbids taking one as a value
	// for a better reason than my test had: "`published` answers whether
	// the target already holds the new content; a caller that cannot tell
	// that from a failed publish cannot choose the right recovery." The
	// seam I wanted would have weakened the guarantee it was meant to
	// prove. atomicfile's own tests own that contract; this file owns
	// that writeManifest goes through it.
}

// The durability step is named and platform-explicit, rather than an
// ignored error at each call site.
func TestDirectorySyncIsOneNamedAbstraction(t *testing.T) {
	dir := t.TempDir()
	if err := atomicfile.SyncDir(dir); err != nil {
		t.Fatalf("syncing an existing directory must succeed on this platform: %v", err)
	}
	// A directory that is not there is an error worth having, not a
	// silent success — that silence is what hid the original defect.
	if err := atomicfile.SyncDir(filepath.Join(dir, "absent")); err == nil && runtime.GOOS != "windows" {
		t.Error("syncing a directory that does not exist must report it")
	}
}
