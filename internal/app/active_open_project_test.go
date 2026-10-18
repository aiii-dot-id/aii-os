package app

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/project"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

func focusFixture(t *testing.T) (*App, *project.Project) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.NewForTest(filepath.Join(dir, "aii.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	a := New(&Config{SourcePath: filepath.Join(dir, "config.json")})
	a.store = st
	a.projects = project.NewManager(filepath.Join(dir, "projects"))
	p, err := a.projects.Create("the-work", "a durable workroom", "operator", nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	return a, p
}

// THE WORKING STATE MUST NOT NAME A CLOSED PROJECT. Three readers — the
// prompt, the boot restore and the dashboard's Active flag — each took
// the stored focus id as the current project without asking whether it
// was still open. The prompt then told the identity "Current project:
// …" and "Where you left off here: …" about a project it had closed,
// which is the working state lying to the resident about where they are.
func TestTheWorkingStateNeverNamesAClosedProject(t *testing.T) {
	a, p := focusFixture(t)
	if _, err := a.selectProject(p.ID); err != nil {
		t.Fatal(err)
	}
	state, err := a.buildWorkState()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(state, "the-work") {
		t.Fatalf("an OPEN focused project must be named — the guard cannot simply refuse everything: %q", state)
	}

	// Force the state the close path is not supposed to leave behind:
	// closed on disk, still focused. This is what the race and the old
	// ordering both produced.
	if _, err := a.projects.SetState(p.ID, "closed"); err != nil {
		t.Fatal(err)
	}
	if err := a.store.SetActiveProject(p.ID); err != nil {
		t.Fatal(err)
	}

	state, err = a.buildWorkState()
	if err != nil {
		t.Fatalf("a closed focus must not fail the prompt, only be left out: %v", err)
	}
	if strings.Contains(state, "Current project") {
		t.Fatalf("THE PROMPT NAMED A CLOSED PROJECT AS CURRENT: %q", state)
	}
}

// The same question, asked at boot. This dropped only a DANGLING id, so
// a closed project survived the restart as the identity's focus.
func TestBootDropsAFocusThatIsClosedNotOnlyOneThatIsGone(t *testing.T) {
	a, p := focusFixture(t)
	if _, err := a.selectProject(p.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.projects.SetState(p.ID, "closed"); err != nil {
		t.Fatal(err)
	}
	if err := a.store.SetActiveProject(p.ID); err != nil {
		t.Fatal(err)
	}

	if _, why := a.activeOpenProject(); why == "" {
		t.Fatal("a closed focus must be reported as one that cannot be honoured")
	} else if !strings.Contains(why, "closed") {
		t.Fatalf("the reason must say WHAT is wrong, for the operator reading the log: %q", why)
	}
}

// CLEARING COMES FIRST. The manifest write is the irreversible half, so
// closing before clearing meant a failed focus-clear left the project
// closed on disk with focus still naming it — and returned "project not
// closed", which was false. It also skipped the lineage line, and
// Manager.SetState refuses the no-op retry, so that line could never be
// written afterwards.
//
// Driven through the real adapter with the store closed underneath it,
// which is the only honest way to make persistence fail here.
func TestAFailedFocusClearLeavesTheProjectOpenAndSaysSo(t *testing.T) {
	a, p := focusFixture(t)
	if _, err := a.selectProject(p.ID); err != nil {
		t.Fatal(err)
	}
	a.store.Close() // every store write from here on fails

	_, err := projectsAdapter{a}.SetState(p.ID, "closed")
	if err == nil {
		t.Fatal("closing must fail when the focus it has to clear cannot be persisted")
	}
	if strings.Contains(err.Error(), "project not closed:") && !strings.Contains(err.Error(), "focus not cleared") {
		t.Fatalf("the old wording claimed the project was not closed while closing it: %v", err)
	}

	// The project is the thing that must not have moved.
	reloaded, lerr := a.projects.Load(p.ID)
	if lerr != nil {
		t.Fatal(lerr)
	}
	if reloaded.State != "open" {
		t.Fatalf("THE PROJECT WAS CLOSED ANYWAY: state %q — the irreversible half ran before the half that could fail", reloaded.State)
	}
}

// The typed contract reaches the resident's working state in a FIXED
// order, with each list in the order it was authored. A claim that moves
// between turns reads as a different claim, and a contract the prompt
// never renders is one the identity has to open a file to learn.
func TestTheWorkingStateRendersTheContractDeterministically(t *testing.T) {
	a, p := focusFixture(t)
	if _, err := a.selectProject(p.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.projects.ApplyPatch(p.ID, nil, nil, nil, nil, &project.Contract{
		Outcome:     "ship the beta on three platforms",
		Acceptance:  []string{"windows installs clean", "the dmg is stapled", "the deb registers"},
		Constraints: []string{"no unsigned binaries", "no network at install time"},
	}, nil); err != nil {
		t.Fatal(err)
	}

	state, err := a.buildWorkState()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(state, "Pursuing: ship the beta on three platforms") {
		t.Fatalf("the outcome must reach the working state:\n%s", state)
	}
	// Authored order preserved, and numbered so a reordering is visible.
	for i, want := range []string{"1. windows installs clean", "2. the dmg is stapled", "3. the deb registers"} {
		if !strings.Contains(state, want) {
			t.Fatalf("acceptance claim %d must render in authored order (%q):\n%s", i+1, want, state)
		}
	}
	if strings.Index(state, "1. windows installs clean") > strings.Index(state, "3. the deb registers") {
		t.Fatalf("acceptance order inverted:\n%s", state)
	}
	// Fixed field order: outcome, then acceptance, then constraints.
	if !(strings.Index(state, "Pursuing:") < strings.Index(state, "Confirmed by:") &&
		strings.Index(state, "Confirmed by:") < strings.Index(state, "Bounded by:")) {
		t.Fatalf("field order must be fixed (outcome, acceptance, constraints):\n%s", state)
	}
	if !strings.Contains(state, "1. no unsigned binaries") {
		t.Fatalf("constraints must render in authored order:\n%s", state)
	}
	// The typed contract is never mirrored into the untyped bag.
	if strings.Contains(state, "attributes") && strings.Contains(state, "outcome") {
		t.Fatalf("the contract must not also appear as an attribute:\n%s", state)
	}
}

// An unauthored contract renders NOTHING — not an empty heading, which
// would read as a contract that says nothing rather than one nobody has
// written yet.
func TestAnUnauthoredContractStaysQuiet(t *testing.T) {
	a, p := focusFixture(t)
	if _, err := a.selectProject(p.ID); err != nil {
		t.Fatal(err)
	}
	state, err := a.buildWorkState()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(state, "Current project") {
		t.Fatalf("the fixture project must still be named:\n%s", state)
	}
	for _, quiet := range []string{"Pursuing:", "Confirmed by:", "Bounded by:", "Part of:"} {
		if strings.Contains(state, quiet) {
			t.Fatalf("an unauthored contract must render nothing, found %q:\n%s", quiet, state)
		}
	}
}
