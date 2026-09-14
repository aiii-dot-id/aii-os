package app

import (
	"path/filepath"
	"strings"
	"testing"
)

// .
// .
// .
// .
// .
func TestRebuildRemedyNamesTheFileAndTheCost(t *testing.T) {
	cfg := Config{}
	cfg.Identity.DBPath = filepath.Join("data", "aii.db")
	got := rebuildRemedy(cfg)

	if !strings.Contains(got, cfg.Identity.DBPath) {
		t.Fatalf("the remedy must name the file to delete; got %q", got)
	}
	// .
	// .
	// .
	if !strings.Contains(got, "conversation history") {
		t.Fatalf("the remedy must state what the rebuild costs; got %q", got)
	}
	// .
	// .
	for _, safe := range []string{"ledger", "key", "projects"} {
		if !strings.Contains(got, safe) {
			t.Fatalf("the remedy must say %s survives; got %q", safe, got)
		}
	}
}

// .
// .
func TestRebuildRemedyFallsBackToTheDefaultPath(t *testing.T) {
	got := rebuildRemedy(Config{})
	if !strings.Contains(got, filepath.Join("data", "aii.db")) {
		t.Fatalf("with no configured DB path the remedy must name the default; got %q", got)
	}
}
