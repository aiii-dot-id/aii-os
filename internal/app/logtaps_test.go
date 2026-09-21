package app

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
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
func TestATapReachesTheSinkFromTheConfigAndTheOverlay(t *testing.T) {
	dir := t.TempDir()
	control := LogControlPathIn(dir)
	t.Cleanup(func() { logsink.SetTaps(nil) })

	// .
	// .
	cfg := Config{Logs: LogsConfig{Dir: "log"}}
	applyLogTaps(cfg, control)
	if logsink.TapEnabled("llm.prompt") {
		t.Fatal("a tap was recording with nothing asking for one")
	}

	// .
	cfg.Logs.Taps = map[string]TapSetting{"llm.prompt": {}}
	applyLogTaps(cfg, control)
	if !logsink.TapEnabled("llm.prompt") {
		t.Fatal("the operator's tap did not reach the sink")
	}

	// .
	// .
	if err := WriteLogControl(control, LogControl{Tap: map[string]string{
		"llm.return":    time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
		"voice.session": time.Now().Add(-time.Hour).UTC().Format(time.RFC3339),
	}}); err != nil {
		t.Fatal(err)
	}
	applyLogTaps(cfg, control)
	if !logsink.TapEnabled("llm.return") {
		t.Error("the identity could not start a capture on itself")
	}
	if logsink.TapEnabled("voice.session") {
		t.Error("a tap whose expiry had already passed started at boot")
	}

	// .
	t.Setenv(logsink.EnvDirective, "error")
	logsink.SetTaps(nil)
	applyLogTaps(cfg, control)
	if !logsink.TapEnabled("llm.prompt") {
		t.Error("setting AII_LOG silently stopped a capture the operator asked for")
	}

	// .
	// .
	// .
	if err := WriteLogControl(control, LogControl{Tap: map[string]string{"llm.prompt": "twenty minutes"}}); err != nil {
		t.Fatal(err)
	}
	logsink.SetTaps(nil)
	applyLogTaps(cfg, control)
	if logsink.TapEnabled("llm.prompt") {
		t.Error("an expiry that is not a time started a recording that would never stop")
	}
}

// .
// .
// .
// .
// .
// .
// .
func TestNoTapIsOnInADefaultConfiguration(t *testing.T) {
	dir := t.TempDir()
	t.Cleanup(func() { logsink.SetTaps(nil) })

	cfg, err := LoadConfig(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Logs.Taps) != 0 {
		t.Fatalf("a default configuration names %d tap(s)", len(cfg.Logs.Taps))
	}
	applyLogTaps(*cfg, LogControlPathIn(dir))
	for _, d := range logsink.Details() {
		if logsink.TapEnabled(d.Name()) {
			t.Errorf("%s is recording in a default configuration", d.Name())
		}
	}
}

// .
// .
// .
// .
// .
func TestAStoppedTapStaysStoppedAgainstAConfigurationThatAsksForIt(t *testing.T) {
	dir := t.TempDir()
	control := LogControlPathIn(dir)
	t.Cleanup(func() { logsink.SetTaps(nil) })

	// .
	cfg := Config{Logs: LogsConfig{Dir: "log", Taps: map[string]TapSetting{"llm.prompt": {}}}}
	applyLogTaps(cfg, control)
	if !logsink.TapEnabled("llm.prompt") {
		t.Fatal("the fixture needs the configuration to turn the tap on")
	}

	// .
	if err := SetLogTap(control, "llm.prompt", TapOff); err != nil {
		t.Fatal(err)
	}
	applyLogTaps(cfg, control)
	if logsink.TapEnabled("llm.prompt") {
		t.Error("A STOPPED TAP IS STILL RECORDING: the configuration asks for it and the stop did not override it")
	}

	// .
	ctl, err := ReadLogControl(control)
	if err != nil {
		t.Fatal(err)
	}
	if got := DescribeLogging(cfg, ctl, control); !strings.Contains(got, "STOPPED by the overlay") {
		t.Errorf("the readback does not report the stop it was asked for:\n%s", got)
	}

	// .
	if err := SetLogTap(control, "llm.prompt", TapClear); err != nil {
		t.Fatal(err)
	}
	applyLogTaps(cfg, control)
	if !logsink.TapEnabled("llm.prompt") {
		t.Error("clear did not hand the question back to the configuration")
	}
}

// .
// .
// .
// .
// .
// .
func TestARejectedOverlayChangesNothing(t *testing.T) {
	dir := t.TempDir()
	control := LogControlPathIn(dir)
	restoreDef, restoreCat := logsink.Levels()
	restoreGroups := logsink.Groups()
	t.Cleanup(func() { logsink.SetLevels(restoreDef, restoreCat); logsink.SetGroups(restoreGroups) })

	cfg := Config{Logs: LogsConfig{
		Dir:    "log",
		Level:  "warn",
		Detail: map[string]string{"llm": "error"},
		Group:  map[string][]string{"fromconfig": {"llm.return"}},
	}}

	// .
	if err := WriteLogControl(control, LogControl{
		Level:  "debug",
		Detail: map[string]string{"voice": "not-a-level"},
		Group:  map[string][]string{"fromoverlay": {"voice.session"}},
	}); err != nil {
		t.Fatal(err)
	}
	applyLogLevels(cfg, control)

	def, cats := logsink.Levels()
	if def != slog.LevelWarn {
		t.Errorf("the rejected overlay published its level anyway: default = %v, want warn", def)
	}
	if _, ok := cats["voice"]; ok {
		t.Error("the rejected overlay published a category anyway")
	}
	if cats["llm"] != slog.LevelError {
		t.Errorf("the operator's own category was disturbed: llm = %v", cats["llm"])
	}
	if g := logsink.Groups(); g["fromoverlay"] != nil {
		t.Error("the rejected overlay published its groups anyway")
	} else if g["fromconfig"] == nil {
		t.Error("the operator's groups did not survive the rejection")
	}

	// .
	// .
	if err := WriteLogControl(control, LogControl{Group: map[string][]string{"stale": {"llm.return"}}}); err != nil {
		t.Fatal(err)
	}
	applyLogLevels(cfg, control)
	if logsink.Groups()["stale"] == nil {
		t.Fatal("the fixture needs the overlay group to be in force first")
	}
	if err := os.WriteFile(control, []byte("{ this is not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	applyLogLevels(cfg, control)
	if logsink.Groups()["stale"] != nil {
		t.Error("an unreadable overlay left the groups of the one it replaced in force")
	}
}
