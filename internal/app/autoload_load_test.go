package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A TRUST THRESHOLD OUTSIDE ITS VALUES IS REFUSED, NOT READ AS THE
// DEFAULT. The settings door refuses a plugins.autoload that is not none,
// T0, T1, T2 or T3. The loader did not check the key: "None" or "t3" in
// the file loaded, each sweep warned and used T1, and the run-time check
// compared with the exact string "none". So a file that asked for nothing
// to load by itself, or for T3 only, activated every package at T1 and
// above.
func TestAnAutoloadLevelOutsideItsValuesIsRefusedAtLoad(t *testing.T) {
	load := func(plugins map[string]any) (*Config, error) {
		t.Helper()
		raw, err := json.Marshal(map[string]any{"plugins": plugins})
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(t.TempDir(), "config.json")
		if err := os.WriteFile(path, raw, 0o600); err != nil {
			t.Fatal(err)
		}
		return ReadConfig(path)
	}
	if cfg, err := load(map[string]any{}); err != nil || cfg.Plugins.Autoload != "T1" {
		t.Fatalf("a file that does not name the level takes the default T1: %v, %+v", err, cfg)
	}
	for _, level := range []string{"none", "T0", "T1", "T2", "T3", ""} {
		cfg, err := load(map[string]any{"autoload": level})
		if err != nil {
			t.Errorf("%q is a level (or empty, for the default), yet the file was refused: %v", level, err)
			continue
		}
		if cfg.Plugins.Autoload != level {
			t.Errorf("%q was read as %q", level, cfg.Plugins.Autoload)
		}
	}
	for _, level := range []string{"None", "t3", "T4", "all", " T1"} {
		_, err := load(map[string]any{"autoload": level})
		if err == nil {
			t.Errorf("%q LOADED: it is read as T1, whatever the file meant by it", level)
			continue
		}
		if !strings.HasPrefix(err.Error(), "plugins.autoload: ") || !strings.Contains(err.Error(), "none, T0, T1, T2, T3") {
			t.Errorf("%q was refused without naming the key and its values: %v", level, err)
		}
	}
}
