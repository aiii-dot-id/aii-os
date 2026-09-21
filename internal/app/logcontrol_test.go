package app

import (
	"log/slog"
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
func TestAnIdentityRaisesItsOwnLevelAndLeavesTheConfigAlone(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	original := []byte(`{"logs":{"dir":"log","level":"warn"}}` + "\n")
	if err := os.WriteFile(cfgPath, original, 0o600); err != nil {
		t.Fatal(err)
	}

	if err := SetLogControl(LogControlPathIn(dir), "debug", map[string]string{"voice": "trace"}); err != nil {
		t.Fatal(err)
	}

	after, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(original) {
		t.Fatalf("the identity rewrote the operator's config:\n  was: %s\n  now: %s", original, after)
	}
	if _, err := os.Stat(LogControlPathIn(dir)); err != nil {
		t.Fatalf("the overlay was not written: %v", err)
	}
}

// .
func TestTheOverlayIsWrittenOwnerOnly(t *testing.T) {
	dir := t.TempDir()
	path := LogControlPathIn(dir)
	if err := SetLogControl(path, "debug", nil); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm&0o077 != 0 {
		t.Fatalf("the overlay is readable beyond its owner: %v", perm)
	}
}

// .
func TestTheEnvironmentBeatsTheOverlayBeatsTheConfig(t *testing.T) {
	dir := t.TempDir()
	control := LogControlPathIn(dir)
	cfg := Config{Logs: LogsConfig{Dir: "log", Level: "warn", Detail: map[string]string{"llm": "error"}}}

	restoreDef, restoreCat := logsink.Levels()
	t.Cleanup(func() { logsink.SetLevels(restoreDef, restoreCat) })

	// .
	applyLogLevels(cfg, control)
	if def, _ := logsink.Levels(); def != slog.LevelWarn {
		t.Fatalf("config alone: default = %v, want warn", def)
	}

	// .
	// .
	if err := SetLogControl(control, "debug", map[string]string{"voice": "trace"}); err != nil {
		t.Fatal(err)
	}
	applyLogLevels(cfg, control)
	def, cats := logsink.Levels()
	if def != slog.LevelDebug {
		t.Fatalf("overlay: default = %v, want debug", def)
	}
	if cats["voice"] != logsink.LevelTrace {
		t.Fatalf("overlay: voice = %v, want trace", cats["voice"])
	}
	if cats["llm"] != slog.LevelError {
		t.Fatalf("the overlay dropped a category it never named: llm = %v, want error", cats["llm"])
	}

	// .
	t.Setenv(logsink.EnvDirective, "error")
	logsink.SetLevels(slog.LevelError, nil)
	applyLogLevels(cfg, control)
	if def, _ := logsink.Levels(); def != slog.LevelError {
		t.Fatalf("with %s set, a file moved the level anyway: %v", logsink.EnvDirective, def)
	}
}

// .
func TestClearingTheOverlayRestoresTheConfig(t *testing.T) {
	dir := t.TempDir()
	control := LogControlPathIn(dir)
	cfg := Config{Logs: LogsConfig{Dir: "log", Level: "warn"}}

	restoreDef, restoreCat := logsink.Levels()
	t.Cleanup(func() { logsink.SetLevels(restoreDef, restoreCat) })

	if err := SetLogControl(control, "trace", nil); err != nil {
		t.Fatal(err)
	}
	applyLogLevels(cfg, control)
	if def, _ := logsink.Levels(); def != logsink.LevelTrace {
		t.Fatalf("overlay did not apply: %v", def)
	}
	if err := ClearLogControl(control); err != nil {
		t.Fatal(err)
	}
	applyLogLevels(cfg, control)
	if def, _ := logsink.Levels(); def != slog.LevelWarn {
		t.Fatalf("after clearing, default = %v, want the config's warn", def)
	}
	if _, err := os.Stat(control); !os.IsNotExist(err) {
		t.Fatalf("the overlay file survived the clear: %v", err)
	}
}

// .
// .
func TestABrokenOverlayLeavesTheOperatorsLevelsStanding(t *testing.T) {
	dir := t.TempDir()
	control := LogControlPathIn(dir)
	if err := os.MkdirAll(filepath.Dir(control), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(control, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	restoreDef, restoreCat := logsink.Levels()
	t.Cleanup(func() { logsink.SetLevels(restoreDef, restoreCat) })

	cap := logsink.CaptureForTest(t)
	applyLogLevels(Config{Logs: LogsConfig{Dir: "log", Level: "warn"}}, control)
	if def, _ := logsink.Levels(); def != slog.LevelWarn {
		t.Fatalf("a broken overlay moved the level: %v", def)
	}
	if !strings.Contains(cap.String(), "unreadable") {
		t.Fatalf("a broken overlay was swallowed silently: %q", cap.String())
	}
}

// .
// .
func TestAnOverlayThatSaysNothingIsRemoved(t *testing.T) {
	dir := t.TempDir()
	control := LogControlPathIn(dir)
	if err := SetLogControl(control, "debug", nil); err != nil {
		t.Fatal(err)
	}
	if err := WriteLogControl(control, LogControl{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(control); !os.IsNotExist(err) {
		t.Fatalf("an empty overlay was left on disk: %v", err)
	}
}

// .
// .
// .
func TestTheReadbackShowsWhichLayerSpeaks(t *testing.T) {
	dir := t.TempDir()
	control := LogControlPathIn(dir)
	cfg := Config{Logs: LogsConfig{Dir: "log", Level: "warn"}}

	plain := DescribeLogging(cfg, LogControl{}, control)
	if !strings.Contains(plain, "no overlay") || !strings.Contains(plain, "level: warn") {
		t.Fatalf("with no overlay the readback should say so plainly:\n%s", plain)
	}

	ctl := LogControl{Level: "debug", Detail: map[string]string{"voice": "trace"}}
	covered := DescribeLogging(cfg, ctl, control)
	for _, want := range []string{"level: debug", "the overlay, over the config", "config:  level warn", "voice=trace", "aii log clear", control} {
		if !strings.Contains(covered, want) {
			t.Fatalf("the readback hides %q:\n%s", want, covered)
		}
	}
}
