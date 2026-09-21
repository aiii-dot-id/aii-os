package app

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/logsink"
)

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
func TestTheOverlayReachesARunningIdentity(t *testing.T) {
	restoreLevels(t)
	t.Setenv(logsink.EnvDirective, "")
	logsink.SetLevels(slog.LevelWarn, nil)

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	body := []byte(`{"logs":{"dir":"log","level":"warn"},"llm":{"provider":"test","model":"m1"}}`)
	if err := os.WriteFile(cfgPath, body, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	a := New(cfg)
	a.live = true
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	a.watchEvery = 10 * time.Minute
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a.bgCtx = ctx
	a.SetForeground(true)

	// .
	// .
	// .
	// .
	// .
	// .
	// .
	if err := os.MkdirAll(filepath.Join(dir, LogControlDirName), 0o700); err != nil {
		t.Fatal(err)
	}

	go a.watchConfig(cfgPath)
	time.Sleep(100 * time.Millisecond)

	if def, _ := logsink.Levels(); def != slog.LevelWarn {
		t.Fatalf("before the overlay the level is %v, want the config's warn", def)
	}

	// .
	if err := SetLogControl(LogControlPathIn(dir), "debug", map[string]string{"voice": "trace"}); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		def, cats := logsink.Levels()
		if def == slog.LevelDebug && cats["voice"] == logsink.LevelTrace {
			// .
			// .
			after, err := os.ReadFile(cfgPath)
			if err != nil {
				t.Fatal(err)
			}
			if string(after) != string(body) {
				t.Fatalf("the running identity rewrote its own config: %s", after)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	def, cats := logsink.Levels()
	t.Fatalf("the overlay never reached the running identity: level=%v voice=%v", def, cats["voice"])
}

// .
// .
func TestClearingTheOverlayReachesARunningIdentity(t *testing.T) {
	restoreLevels(t)
	t.Setenv(logsink.EnvDirective, "")

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(cfgPath, []byte(`{"logs":{"dir":"log","level":"warn"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SetLogControl(LogControlPathIn(dir), "debug", nil); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	a := New(cfg)
	a.live = true
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	a.watchEvery = 10 * time.Minute
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a.bgCtx = ctx
	a.SetForeground(true)

	applyLogLevels(*cfg, LogControlPathIn(dir))
	if def, _ := logsink.Levels(); def != slog.LevelDebug {
		t.Fatalf("the overlay did not apply at boot: %v", def)
	}

	go a.watchConfig(cfgPath)
	time.Sleep(100 * time.Millisecond)

	// .
	// .
	// .
	if err := ClearLogControl(LogControlPathIn(dir)); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if def, _ := logsink.Levels(); def == slog.LevelWarn {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	def, _ := logsink.Levels()
	t.Fatalf("clearing the overlay never reached the running identity: level=%v", def)
}
