package app

import (
	"bytes"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestUILayoutInertProfileWarns pins the tolerate-AND-log rule.
//
// An unknown profile name is KEPT on purpose: a layout written for a
// newer frame must survive a downgrade. But the frame selects by
// viewport and can only ever ask for the names in
// selectableUIProfiles, so any other profile is parsed, stored,
// transmitted, and never rendered. Tolerance without a word is
// indistinguishable from a typo being swallowed.
//
// This test exists because the warning shipped as an UNEXECUTED log
// line: every other layout test calls loadUILayout(true) — quiet —
// so the branch had never run. A log line nothing executes is prose
// that compiles.
func TestUILayoutInertProfileWarns(t *testing.T) {
	dir := t.TempDir()
	a := New(&Config{Identity: IdentityConfig{LedgerPath: filepath.Join(dir, "ledger.jsonl")}})
	path := a.uiLayoutPath()

	var logBuf bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&logBuf)
	defer log.SetOutput(prev)

	// A layout whose profiles are ALL selectable must produce no inert
	// line. This is the negative control: it proves the assertion below
	// discriminates rather than matching any load at all.
	onlySelectable := `{"v":1,"profiles":{"desktop":{"panel":["hello"]},"mobile":{"dock":["hello"]}}}`
	if err := os.WriteFile(path, []byte(onlySelectable), 0o644); err != nil {
		t.Fatal(err)
	}
	if !a.loadUILayout(false) {
		t.Fatal("a fresh valid layout must register as changed")
	}
	if strings.Contains(logBuf.String(), "INERT") {
		t.Fatalf("selectable-only profiles must not warn, got: %s", logBuf.String())
	}

	// Now the real case: a profile the frame can never ask for.
	logBuf.Reset()
	withTablet := `{"v":1,"profiles":{"desktop":{"panel":["hello"]},"tablet":{"panel":["hello"]}}}`
	if err := os.WriteFile(path, []byte(withTablet), 0o644); err != nil {
		t.Fatal(err)
	}
	if !a.loadUILayout(false) {
		t.Fatal("the changed layout must register as changed")
	}

	got := logBuf.String()
	if !strings.Contains(got, "INERT") {
		t.Fatalf("an unselectable profile must be announced, got: %s", got)
	}
	if !strings.Contains(got, `"tablet"`) {
		t.Fatalf("the warning must name the offending profile, got: %s", got)
	}
	// The warning must name what the frame CAN select, or it tells the
	// operator something is wrong without telling them what to write.
	if !strings.Contains(got, "desktop") || !strings.Contains(got, "mobile") {
		t.Fatalf("the warning must name the selectable profiles, got: %s", got)
	}
	// It must not slander the profile that IS selectable.
	if strings.Contains(got, `"desktop" is kept but INERT`) {
		t.Fatalf("a selectable profile must never be called inert, got: %s", got)
	}

	// TOLERANCE: the whole file is still stored, tablet included. The
	// rule is tolerate-and-log, not warn-and-discard — a downgrade must
	// keep the newer frame's layout intact.
	if string(a.currentUILayout()) != withTablet {
		t.Fatalf("an inert profile must be KEPT, not dropped; stored: %s", a.currentUILayout())
	}
}
