package app

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/logsink"
)

func writeLogConfig(t *testing.T, dir string, logs map[string]any) string {
	t.Helper()
	path := filepath.Join(dir, "config.json")
	body := map[string]any{"logs": logs}
	raw, err := json.MarshalIndent(body, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func restoreLevels(t *testing.T) {
	t.Helper()
	def, cats := logsink.Levels()
	t.Cleanup(func() { logsink.SetLevels(def, cats) })
}

// .
// .
func TestTheConfigsLevelsReachTheSink(t *testing.T) {
	restoreLevels(t)
	t.Setenv(logsink.EnvDirective, "")

	path := writeLogConfig(t, t.TempDir(), map[string]any{
		"dir": "log", "level": "warn", "detail": map[string]string{"route.renewal": "trace"},
	})
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	applyLogLevels(*cfg, LogControlPathIn(filepath.Dir(path)))

	def, cats := logsink.Levels()
	if def != slog.LevelWarn {
		t.Fatalf("default level = %v, want warn", def)
	}
	if cats["route.renewal"] != logsink.LevelTrace {
		t.Fatalf("route.renewal = %v, want trace", cats["route.renewal"])
	}
}

// .
// .
func TestAnEnvironmentDirectiveWinsForItsRun(t *testing.T) {
	restoreLevels(t)
	logsink.SetLevels(logsink.LevelTrace, map[string]slog.Level{"llm": slog.LevelDebug})
	t.Setenv(logsink.EnvDirective, "warn")

	path := writeLogConfig(t, t.TempDir(), map[string]any{"dir": "log", "level": "error"})
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	applyLogLevels(*cfg, LogControlPathIn(filepath.Dir(path)))

	if def, _ := logsink.Levels(); def != logsink.LevelTrace {
		t.Fatalf("the config overrode an environment directive: level = %v", def)
	}
}

// .
// .
// .
// .
// .
// .
// .
func TestWhatTheCommandWritesReachesARunningIdentity(t *testing.T) {
	restoreLevels(t)
	t.Setenv(logsink.EnvDirective, "")
	logsink.SetLevels(slog.LevelInfo, nil)

	dir := t.TempDir()
	path := writeLogConfig(t, dir, map[string]any{"dir": "log"})

	// .
	if err := SetLogControl(LogControlPathIn(dir), "debug", map[string]string{"voice": "trace"}); err != nil {
		t.Fatalf("the command's write: %v", err)
	}

	// .
	fresh, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	applyLogLevels(*fresh, LogControlPathIn(filepath.Dir(path)))

	def, cats := logsink.Levels()
	if def != slog.LevelDebug || cats["voice"] != logsink.LevelTrace {
		t.Fatalf("the write did not reach the sink: level=%v voice=%v", def, cats["voice"])
	}

	// .
	if err := SetLogControl(LogControlPathIn(dir), "", map[string]string{"voice": ""}); err != nil {
		t.Fatal(err)
	}
	fresh, _ = LoadConfig(path)
	applyLogLevels(*fresh, LogControlPathIn(filepath.Dir(path)))
	if _, still := logsink.Levels(); still["voice"] != 0 {
		t.Fatalf("clearing an override left it behind: %v", still)
	}
	if def, _ := logsink.Levels(); def != slog.LevelDebug {
		t.Fatalf("clearing an override changed the default: %v", def)
	}
}

// .
// .
func TestARefusedLevelWritesNothing(t *testing.T) {
	restoreLevels(t)
	dir := t.TempDir()
	path := writeLogConfig(t, dir, map[string]any{"dir": "log", "level": "info"})
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := SetLogControl(LogControlPathIn(dir), "chatty", nil); err == nil {
		t.Fatal("a word that is not a level must be refused")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("a refused write changed the operator's config: %s", after)
	}
	if _, err := os.Stat(LogControlPathIn(dir)); !os.IsNotExist(err) {
		t.Fatalf("a refused write left an overlay behind: %v", err)
	}
}

// .
// .
func TestDescribeLoggingSaysWhatIsInForceAndWhereTheRecordIs(t *testing.T) {
	restoreLevels(t)
	t.Setenv(logsink.EnvDirective, "")
	path := writeLogConfig(t, t.TempDir(), map[string]any{
		"dir": "log", "level": "debug", "detail": map[string]string{"route.renewal": "trace"},
	})
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	out := DescribeLogging(*cfg, LogControl{}, LogControlPathIn(filepath.Dir(path)))
	for _, want := range []string{"level: debug", "route.renewal=trace", "log/" + logsink.LiveName} {
		if !contains(out, want) {
			t.Fatalf("the description is missing %q:\n%s", want, out)
		}
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (func() bool {
		for i := 0; i+len(needle) <= len(haystack); i++ {
			if haystack[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}
