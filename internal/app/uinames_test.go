package app

import (
	"os"
	"path/filepath"
	"testing"
)

// .
// .
// .

func newNamesApp(t *testing.T) (*App, string) {
	t.Helper()
	dir := t.TempDir()
	led := filepath.Join(dir, "ledger.jsonl")
	a := New(&Config{Identity: IdentityConfig{LedgerPath: led}})
	a.snapshotUILayoutPath(led)
	// .
	// .
	if err := os.MkdirAll(a.uiOverlayDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	return a, filepath.Join(a.uiOverlayDir(), "name")
}

func TestResolveDisplayNameFileWins(t *testing.T) {
	a, nameFile := newNamesApp(t)
	if got := a.resolveDisplayName(); got != "" {
		t.Fatalf("absent file: want cfg fallback, got %q", got)
	}
	if err := os.WriteFile(nameFile, []byte("Aurora\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := a.resolveDisplayName(); got != "Aurora" {
		t.Fatalf("file present: want Aurora, got %q", got)
	}
	if err := os.Remove(nameFile); err != nil {
		t.Fatal(err)
	}
	if got := a.resolveDisplayName(); got != "" {
		t.Fatalf("deleted file: want cfg fallback (deletion is the undo), got %q", got)
	}
}

func TestResolveDisplayNameGate(t *testing.T) {
	a, nameFile := newNamesApp(t)
	bad := map[string]string{
		"blank":          "   \n",
		"overlong-bytes": string(make([]byte, 300)) + "\n",
		"overlong-runes": string(make([]rune, 80)) + "\n",
		"control-rune":   "Bad\x00Name\n",
		"bidi-override":  "Evil\u202eName\n",
	}
	for label, content := range bad {
		if err := os.WriteFile(nameFile, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := a.resolveDisplayName(); got != "" {
			t.Fatalf("%s: want refusal (cfg fallback), got %q", label, got)
		}
	}
	// .
	// .
	// .
	if err := os.WriteFile(nameFile, []byte("<b>Sam</b>\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := a.resolveDisplayName(); got != "<b>Sam</b>" {
		t.Fatalf("markup: want literal text, got %q", got)
	}
}
