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

// THE OVERLAY REACHES AN IDENTITY THAT IS ALREADY RUNNING.
//
// This is the falsifier for the whole control plane. Everything else
// here tests the file and the precedence; this drives the REAL watcher
// and proves that `aii log level debug`, typed by an operator or run by
// the identity through its own shell, changes what a live process tells
// without a restart and without anyone touching config.json.
//
// It also proves the narrower thing the watcher was written for: an
// overlay change must not run the whole configuration reload. The model
// in the config is left alone here, and a reload would have re-read and
// re-applied it.
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
	a.live.Store(true)
	// A heartbeat far longer than this test can run. fsdir is
	// event-driven with the heartbeat as drift insurance, so this leaves
	// the EVENT as the only thing that can carry the change — and that
	// is the whole claim. At a 20ms heartbeat the config watcher wakes
	// constantly and reloadConfig applies the overlay on its way past,
	// which made an earlier version of this test pass with the control
	// watcher deleted outright.
	a.watchEvery = 10 * time.Minute
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a.bgCtx = ctx
	a.SetForeground(true)

	// The log directory is made BEFORE the watcher starts, and this is
	// not tidiness. WriteLogControl creates it on demand, and creating a
	// subdirectory inside the watched directory wakes the CONFIG watcher,
	// whose reload applies the overlay on its way past — so without this
	// the test passes with the control watcher removed entirely, proving
	// nothing. In production the directory already exists for the same
	// reason: watchConfig makes it before it starts watching.
	if err := os.MkdirAll(filepath.Join(dir, LogControlDirName), 0o700); err != nil {
		t.Fatal(err)
	}

	go a.watchConfig(cfgPath)
	time.Sleep(100 * time.Millisecond) // the watcher takes its baseline

	if def, _ := logsink.Levels(); def != slog.LevelWarn {
		t.Fatalf("before the overlay the level is %v, want the config's warn", def)
	}

	// Exactly what the command writes, at exactly the path it writes to.
	if err := SetLogControl(LogControlPathIn(dir), "debug", map[string]string{"voice": "trace"}); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		def, cats := logsink.Levels()
		if def == slog.LevelDebug && cats["voice"] == logsink.LevelTrace {
			// The config was never rewritten, and the reload path that
			// would have swapped the client never ran.
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

// AND CLEARING IT REACHES THEM TOO: an identity that raised its own
// level to look at a fault must be able to put it back.
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
	a.live.Store(true)
	// A heartbeat far longer than this test can run. fsdir is
	// event-driven with the heartbeat as drift insurance, so this leaves
	// the EVENT as the only thing that can carry the change — and that
	// is the whole claim. At a 20ms heartbeat the config watcher wakes
	// constantly and reloadConfig applies the overlay on its way past,
	// which made an earlier version of this test pass with the control
	// watcher deleted outright.
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

	// Clearing removes a file inside a directory that already exists, so
	// the parent is undisturbed and only the control watcher can carry
	// this — which is the point of the test.
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
