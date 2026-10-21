package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/logsink"
)

// logs.max_bytes IS THE OPERATOR'S, AND HELD TO ITS RANGE WHERE IT IS
// WRITTEN. The size at which the live log rolls is a setting beside
// max_backups and max_days: absent or zero takes the sink's default, a
// number under the floor or over the ceiling is refused by its key at
// load and at the settings door, the sink is handed what the file says,
// and the page is told it where it is told its neighbours.
func TestLogsMaxBytesIsHeldToItsFloorAtLoadAndAtTheDoor(t *testing.T) {
	load := func(logs string) (*Config, error) {
		path := filepath.Join(t.TempDir(), "config.json")
		if err := os.WriteFile(path, []byte(`{"logs":{"dir":"log"`+logs+`}}`), 0o600); err != nil {
			t.Fatal(err)
		}
		return ReadConfig(path)
	}
	for _, logs := range []string{``, `,"max_bytes":0`} {
		cfg, err := load(logs)
		if err != nil || cfg.Logs.MaxBytes != 0 {
			t.Fatalf("logs %q: %v, max_bytes %d; want it to load and name no size", logs, err, cfg.Logs.MaxBytes)
		}
		if got := New(cfg).sinkConfig().MaxBytes; got != 0 {
			t.Fatalf("logs %q: the sink was handed %d, want 0 for its default", logs, got)
		}
	}
	// The refusal names the default as the constant holds it, never as a
	// number of its own.
	theDefault := fmt.Sprintf("(take the default, %d)", int64(logsink.DefaultMaxBytes))
	for _, n := range []string{"-1", "1", "1048575", "33554433", "68719476736"} {
		_, err := load(`,"max_bytes":` + n)
		if err == nil || !strings.Contains(err.Error(), "logs.max_bytes") {
			t.Errorf("max_bytes %s loaded, or was refused without its key: %v", n, err)
		} else if !strings.Contains(err.Error(), theDefault) {
			t.Errorf("THE REFUSAL OF max_bytes %s DOES NOT STATE THE DEFAULT THE SINK TAKES, %d: %v", n, int64(logsink.DefaultMaxBytes), err)
		}
	}
	cfg, err := load(`,"max_bytes":1048576`)
	if err != nil {
		t.Fatalf("the floor itself was refused: %v", err)
	}
	if got := New(cfg).sinkConfig().MaxBytes; got != logsink.MinMaxBytes {
		t.Fatalf("the sink was handed %d, the file says %d", got, int64(logsink.MinMaxBytes))
	}
	if _, err := load(`,"max_bytes":33554432`); err != nil {
		t.Fatalf("the ceiling itself was refused: %v", err)
	}

	a, _ := retiredKeyApp(t)
	if _, err := a.applyConfigChange(map[string]any{"logs.max_bytes": float64(4096)}); err == nil || !strings.Contains(err.Error(), "logs.max_bytes") || !strings.Contains(err.Error(), theDefault) {
		t.Fatalf("the door took a size under the floor, or refused it without the default the sink takes: %v", err)
	}
	if _, err := a.applyConfigChange(map[string]any{"logs.max_bytes": float64(2 << 20)}); err != nil {
		t.Fatalf("the door refused a size over the floor: %v", err)
	}
	if got := a.configSnapshot().Logs.MaxBytes; got != 2<<20 {
		t.Fatalf("the door did not keep the size: %d", got)
	}
	if _, err := a.applyConfigChange(map[string]any{"logs.max_bytes": float64(logsink.MaxMaxBytes + 1)}); err == nil || !strings.Contains(err.Error(), "logs.max_bytes") {
		t.Fatalf("the door took a size over the ceiling: %v", err)
	}
	if got := a.configState().Logs; got.MaxBytes != 2<<20 || got.MaxBytesDefault != logsink.DefaultMaxBytes {
		t.Fatalf("the page is told a size of %d and a default of %d, want %d and %d", got.MaxBytes, got.MaxBytesDefault, 2<<20, int64(logsink.DefaultMaxBytes))
	}
	// The Logs card's label prints what the page is told under this name.
	if got := readBack(t, a.configState(), "logs.max_bytes_default"); got != float64(logsink.DefaultMaxBytes) {
		t.Fatalf("THE PAGE'S LABEL IS NOT GIVEN THE DEFAULT THE SINK TAKES: logs.max_bytes_default reads %v, want %d", got, int64(logsink.DefaultMaxBytes))
	}
	if _, err := a.applyConfigChange(map[string]any{"logs.max_bytes": nil}); err != nil {
		t.Fatalf("the door refused to put the default back: %v", err)
	}
	if got := a.configSnapshot().Logs.MaxBytes; got != 0 {
		t.Fatalf("null did not put the default back: %d", got)
	}
}

// logs.max_total_bytes IS THE OPERATOR'S TOO. Absent or zero
// takes the sink's default, a bound under the roll size in force, or
// negative, is refused by its key at load and at the settings door (where
// it is held to the roll size once both have landed; at least twice it),
// the sink is handed
// what the file says, and the page is told it and the default it stands
// for.
func TestLogsMaxTotalBytesIsHeldToTheRollSizeAtLoadAndAtTheDoor(t *testing.T) {
	load := func(logs string) (*Config, error) {
		path := filepath.Join(t.TempDir(), "config.json")
		if err := os.WriteFile(path, []byte(`{"logs":{"dir":"log"`+logs+`}}`), 0o600); err != nil {
			t.Fatal(err)
		}
		return ReadConfig(path)
	}
	for _, logs := range []string{``, `,"max_total_bytes":0`} {
		cfg, err := load(logs)
		if err != nil || cfg.Logs.MaxTotalBytes != 0 || New(cfg).sinkConfig().MaxTotalBytes != 0 {
			t.Fatalf("logs %q: %v; want it to load and hand the sink no bound of its own", logs, err)
		}
	}
	for _, logs := range []string{`,"max_total_bytes":-1`, `,"max_total_bytes":1048576`, `,"max_bytes":2097152,"max_total_bytes":4194303`} {
		if _, err := load(logs); err == nil || !strings.Contains(err.Error(), "logs.max_total_bytes") {
			t.Errorf("logs %q loaded, or was refused without its key: %v", logs, err)
		}
	}
	cfg, err := load(`,"max_bytes":2097152,"max_total_bytes":4194304`)
	if err != nil || New(cfg).sinkConfig().MaxTotalBytes != 4194304 {
		t.Fatalf("a bound of two rolled files was refused or not handed to the sink: %v", err)
	}

	a, _ := retiredKeyApp(t)
	if _, err := a.applyConfigChange(map[string]any{"logs.max_total_bytes": float64(4096)}); err == nil || !strings.Contains(err.Error(), "logs.max_total_bytes") {
		t.Fatalf("the door took a bound under the roll size: %v", err)
	}
	// Both in one save: held to the roll size the save sets, whatever the
	// order the keys come in.
	if _, err := a.applyConfigChange(map[string]any{"logs.max_bytes": float64(2 << 20), "logs.max_total_bytes": float64(5 << 20)}); err != nil {
		t.Fatalf("the door refused a bound over the roll size the same save set: %v", err)
	}
	if _, err := a.applyConfigChange(map[string]any{"logs.max_bytes": float64(4 << 20)}); err == nil || !strings.Contains(err.Error(), "logs.max_total_bytes") {
		t.Fatalf("the door took a roll size over the folder's bound: %v", err)
	}
	if got := a.configState().Logs; got.MaxTotalBytes != 5<<20 || got.MaxTotalBytesDefault != logsink.DefaultMaxTotalBytes(2<<20, a.configSnapshot().Logs.MaxBackups) {
		t.Fatalf("the page is told a bound of %d and a default of %d", got.MaxTotalBytes, got.MaxTotalBytesDefault)
	}
	if _, err := a.applyConfigChange(map[string]any{"logs.max_total_bytes": nil}); err != nil || a.configSnapshot().Logs.MaxTotalBytes != 0 {
		t.Fatalf("null did not put the default back: %v", err)
	}
}
