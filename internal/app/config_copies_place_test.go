package app

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestTheConfigurationsCopiesAreKeptByNameAndUnderASecondName(t *testing.T) {
	copies := []string{"config.json.before-update", "config.json.before-0.2.1", "config.json.rolled-back",
		"providers.json.before-update", "providers.json.before-0.2.1", "providers.json.rolled-back"}
	app, home := stockSlotApp(t, map[string]string{"notes/mine.md": "mine"})
	// Written once the identity runs: a healthy boot drops the copies of a
	// settled update, and these stand for one that is not.
	for _, c := range copies {
		if err := os.WriteFile(filepath.Join(home, c), []byte("SECRET "+c), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// The wall keeps each file's copies by the stem <file>. as written.
	// Resolved as a file, Windows read config.json. as config.json, the
	// file itself, and the stem was dropped as inside the file's place.
	kept := app.ensureRing5Policy().Kept()
	for _, stem := range []string{"config.json.", "providers.json."} {
		if !slices.ContainsFunc(kept, func(p string) bool { return filepath.Base(p) == stem }) {
			t.Errorf("the wall keeps no stem %s: %q", stem, kept)
		}
	}
	for i, c := range copies {
		second := filepath.Join(home, "notes", "second-"+string(rune('a'+i))+".txt")
		if err := os.Link(filepath.Join(home, c), second); err != nil {
			t.Skipf("hard links unavailable here: %v", err)
		}
		for _, p := range []string{c, filepath.Join(home, c), second} {
			if out := toolRead(app, p); strings.Contains(out, "SECRET") || !strings.Contains(out, "access denied: protected path") {
				t.Errorf("THE IDENTITY'S READ OPENED %s, A COPY OF THE CONFIGURATION (%s): %q", p, c, out)
			}
		}
		if out := toolWrite(app, second, "SPRAY"); !strings.Contains(out, "access denied: protected path") {
			t.Errorf("the identity's write of %s, a second name for %s, must be refused: %q", second, c, out)
		}
	}
	if raw, err := os.ReadFile(filepath.Join(home, "config.json.before-update")); err != nil || string(raw) != "SECRET config.json.before-update" {
		t.Fatalf("a copy was written through a second name: %q %v", raw, err)
	}
	if out := toolWrite(app, "config.json.before-9.9.9", "MADE"); !strings.Contains(out, "access denied: protected path") {
		t.Errorf("a copy's name not made yet must be refused: %q", out)
	}
	// The floor the identity reads is as it was: each file named once,
	// its copies under its line and not by their own names.
	floor := app.ensureRing5Policy().LocalFloor()
	if strings.Count(floor, "**config.json**") != 1 || strings.Count(floor, "**providers.json**") != 1 || strings.Count(floor, "config.json") != 1 || strings.Count(floor, "providers.json") != 1 {
		t.Errorf("the identity's floor names the copies apart from the files they copy:\n%s", floor)
	}
	if out := toolRead(app, "notes/mine.md"); !strings.Contains(out, "mine") {
		t.Errorf("the home stays the identity's: %q", out)
	}
}
