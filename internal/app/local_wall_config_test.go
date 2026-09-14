package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// .
func TestLocalSpawnWallDefaultsAndValidates(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.json")
	if err := os.WriteFile(p, []byte(`{"agency":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Agency.SubagentWallSecondsLocal != 1800 {
		t.Fatalf("default local wall = %d, want 1800", cfg.Agency.SubagentWallSecondsLocal)
	}
	if err := os.WriteFile(p, []byte(`{"agency":{"subagent_wall_seconds_local":-1}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(p); err == nil || !strings.Contains(err.Error(), "subagent_wall_seconds_local must be positive") {
		t.Fatalf("a negative local wall was accepted: %v", err)
	}
}
