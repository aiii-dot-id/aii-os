package app

import (
	"os"
	"path/filepath"
	"testing"
)

// THE OPERATOR'S EXCERPT AND COUNT OF RECENT EXPERIENCES REACH THE
// FACILITIES THAT USE THEM (prompt.excerpt_chars,
// prompt.ring3_recent_experiences). Both rows of the settings table set the
// cost of every prompt; from config.json they reach DREAM's contradiction
// view and CONSOLIDATE's view, retired beliefs, outcomes and working truth.
// The table's own tests walk the door, the read-back and the held values.
func TestThePromptCostSettingsReachTheFacilities(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"prompt":{"recent_turns":20,"excerpt_chars":60,"ring3_recent_experiences":2}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := dreamConfig(*cfg).ExcerptChars; got != 60 {
		t.Errorf("DREAM's excerpt is %d; the file says 60", got)
	}
	if got := consolidateConfig(*cfg); got.ExcerptChars != 60 || got.RecentExperiences != 2 {
		t.Errorf("CONSOLIDATE's excerpt and recent count are %d and %d; the file says 60 and 2", got.ExcerptChars, got.RecentExperiences)
	}
}
