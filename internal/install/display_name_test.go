package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
)

func writeUIName(t *testing.T, slotDir, content string) {
	t.Helper()
	dir := filepath.Join(slotDir, "data", "ui")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "name"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// The runtime resolves data/ui/name > ledger genesis. If the CLI
// followed only the ledger, `aii slots` would name an identity
// differently from the dashboard the moment they named themselves.
func TestDisplayNameOverridesTheBirthName(t *testing.T) {
	root := t.TempDir()
	dir, err := Create(root, 0)
	if err != nil {
		t.Fatal(err)
	}
	writeLedger(t, dir, record(t, 1, ledger.EventRing0Genesis, `{"name":"TestIdentity"}`))
	if got := Name(dir); got != "TestIdentity" {
		t.Fatalf("with no display name, Name = %q, want the birth name %q", got, "TestIdentity")
	}

	writeUIName(t, dir, "TestIdentity of the Long Watch\n")
	if got := Name(dir); got != "TestIdentity of the Long Watch" {
		t.Fatalf("Name = %q, want the display name", got)
	}

	// Deleting the file is the undo — the birth name returns.
	if err := os.Remove(filepath.Join(dir, "data", "ui", "name")); err != nil {
		t.Fatal(err)
	}
	if got := Name(dir); got != "TestIdentity" {
		t.Fatalf("after removing the override, Name = %q, want %q", got, "TestIdentity")
	}
}

// A name the runtime REFUSES to render must not appear here either, or
// the two disagree about who someone is. Each of these is refused by
// internal/app's gate.
func TestRefusedDisplayNamesFallBackToBirth(t *testing.T) {
	cases := map[string]string{
		"empty":          "\n",
		"blank":          "   \n",
		"control rune":   "Wr\x07en\n",
		"bidi override":  "TestIdentity\u202e\n",
		"too many runes": strings.Repeat("x", 65) + "\n",
		"oversize file":  strings.Repeat("y", 300),
	}
	for label, content := range cases {
		t.Run(label, func(t *testing.T) {
			root := t.TempDir()
			dir, err := Create(root, 0)
			if err != nil {
				t.Fatal(err)
			}
			writeLedger(t, dir, record(t, 1, ledger.EventRing0Genesis, `{"name":"TestPeer"}`))
			writeUIName(t, dir, content)
			if got := Name(dir); got != "TestPeer" {
				t.Fatalf("a refused display name (%s) was shown as %q; want the birth name %q", label, got, "TestPeer")
			}
		})
	}
}

// Only the first line counts, as the runtime's Cut does.
func TestDisplayNameTakesTheFirstLineOnly(t *testing.T) {
	root := t.TempDir()
	dir, err := Create(root, 0)
	if err != nil {
		t.Fatal(err)
	}
	writeLedger(t, dir, record(t, 1, ledger.EventRing0Genesis, `{"name":"TestPeer"}`))
	writeUIName(t, dir, "Aster\nignored second line\n")
	if got := Name(dir); got != "Aster" {
		t.Fatalf("Name = %q, want %q", got, "Aster")
	}
}
