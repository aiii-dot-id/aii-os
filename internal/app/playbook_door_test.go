package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// THE PLAYBOOK IS READ THROUGH THE READ DOOR. The host read it with
// os.ReadFile, so a symlink the identity made at its path put a kept file
// (here a credential in the data directory) into the work start's reply
// and onto the card. Both now read it as the
// read tool does: the kept file is refused, nothing of it is shown, and
// the refusal is said; a plain playbook is shown as before.
func TestAPlaybookLinkedToAKeptFileShowsTheRefusalNotTheFile(t *testing.T) {
	app, home := stockSlotApp(t, map[string]string{"data/credentials/anthropic.json": "SECRET credential"})
	book := filepath.Join(home, playbookRel("leak"))
	if err := os.MkdirAll(filepath.Dir(book), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(home, "data", "credentials", "anthropic.json"), book); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if block := app.playbookBlock(ctx, "leak"); strings.Contains(block, "SECRET") || !strings.Contains(block, "could not be read: access denied") && !strings.Contains(block, "could not be read: protected path") {
		t.Fatalf("a playbook linked to a kept file must show the refusal, not the file: %q", block)
	}
	plain := filepath.Join(home, playbookRel("plain"))
	if err := os.MkdirAll(filepath.Dir(plain), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(plain, []byte("1. read the log\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if block := app.playbookBlock(ctx, "plain"); !strings.Contains(block, "your own file") || !strings.Contains(block, "1. read the log") {
		t.Fatalf("a plain playbook is shown: %q", block)
	}
	if block := app.playbookBlock(ctx, "none"); block != "" {
		t.Fatalf("no playbook shows nothing: %q", block)
	}
}
