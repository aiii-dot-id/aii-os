package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/logsink"
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
// .
func TestUILayoutInertProfileWarns(t *testing.T) {
	dir := t.TempDir()
	a := New(&Config{Identity: IdentityConfig{LedgerPath: filepath.Join(dir, "ledger.jsonl")}})
	path := a.uiLayoutPath()

	logBuf := logsink.CaptureForTest(t)

	// .
	// .
	// .
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

	// .
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
	// .
	// .
	if !strings.Contains(got, "desktop") || !strings.Contains(got, "mobile") {
		t.Fatalf("the warning must name the selectable profiles, got: %s", got)
	}
	// .
	if strings.Contains(got, `"desktop" is kept but INERT`) {
		t.Fatalf("a selectable profile must never be called inert, got: %s", got)
	}

	// .
	// .
	// .
	if string(a.currentUILayout()) != withTablet {
		t.Fatalf("an inert profile must be KEPT, not dropped; stored: %s", a.currentUILayout())
	}
}
