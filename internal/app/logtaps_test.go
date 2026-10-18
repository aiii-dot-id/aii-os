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

// logtaps_test.go — a tap reaches the sink through the operator's
// durable list and the identity's overlay, and through its OWN pass.

// A TAP IS NOT A LEVEL. It arrives by the same two layers, but by a
// separate application: AII_LOG names what this launch tells, and an
// environment variable about levels must not decide whether an identity
// is being recorded.
func TestATapReachesTheSinkFromTheConfigAndTheOverlay(t *testing.T) {
	dir := t.TempDir()
	control := LogControlPathIn(dir)
	t.Cleanup(func() { logsink.SetTaps(nil) })

	// Off is the default, and the state after every boot not told
	// otherwise.
	cfg := Config{Logs: LogsConfig{Dir: "log"}}
	applyLogTaps(cfg, control)
	if logsink.TapEnabled("llm.prompt") {
		t.Fatal("a tap was recording with nothing asking for one")
	}

	// The operator's durable list.
	cfg.Logs.Taps = map[string]TapSetting{"llm.prompt": {}}
	applyLogTaps(cfg, control)
	if !logsink.TapEnabled("llm.prompt") {
		t.Fatal("the operator's tap did not reach the sink")
	}

	// The identity's own overlay adds a return tap (a prompt tap is the
	// operator's alone), and one whose expiry has already passed is not
	// resumed.
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

	// AII_LOG pins this launch's levels. It does not turn a capture off.
	t.Setenv(logsink.EnvDirective, "error")
	logsink.SetTaps(nil)
	applyLogTaps(cfg, control)
	if !logsink.TapEnabled("llm.prompt") {
		t.Error("setting AII_LOG silently stopped a capture the operator asked for")
	}

	// AND A TYPO IS NOT "FOREVER". Reading an unparseable stamp as "no
	// expiry" would turn one wrong character into a recording that never
	// stops, which is the single failure this surface must not have.
	// (A return tap: the overlay cannot start a prompt tap at all.)
	if err := WriteLogControl(control, LogControl{Tap: map[string]string{"llm.return": "twenty minutes"}}); err != nil {
		t.Fatal(err)
	}
	logsink.SetTaps(nil)
	applyLogTaps(cfg, control)
	if logsink.TapEnabled("llm.return") {
		t.Error("an expiry that is not a time started a recording that would never stop")
	}
}

func TestFacilityPromptTapIsASelectableDetail(t *testing.T) {
	control := LogControlPathIn(t.TempDir())
	t.Cleanup(func() { logsink.SetTaps(nil) })
	// A prompt tap is the operator's: it is started in config.json.
	applyLogTaps(Config{Logs: LogsConfig{Taps: map[string]TapSetting{"dream.prompt": {}}}}, control)
	if !logsink.TapEnabled("dream.prompt") || logsink.TapEnabled("llm.prompt") {
		t.Fatal("the facility selector did not stay narrower than the broad LLM tap")
	}
	if err := SetLogTap(control, "identity_review.prompt", ""); err == nil {
		t.Fatal("a facility with no LLM call cannot enable a nonexistent prompt tap")
	}
}

// A CAPTURE IS NEVER ON BY DEFAULT, and no upgrade turns one on. The
// raw prompts and answers of an identity are not something a host
// begins recording because a version changed: it takes an operator or
// the identity saying so, in a file, with a line in the log to show for
// it. This walks every declared detail rather than the two that have
// call sites today, so a payload detail added later cannot arrive
// switched on.
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

func TestAStoppedTapStaysStoppedAgainstAConfigurationThatAsksForIt(t *testing.T) {
	dir := t.TempDir()
	control := LogControlPathIn(dir)
	t.Cleanup(func() { logsink.SetTaps(nil) })

	// The operator's durable list asks for it.
	cfg := Config{Logs: LogsConfig{Dir: "log", Taps: map[string]TapSetting{"llm.prompt": {}}}}
	applyLogTaps(cfg, control)
	if !logsink.TapEnabled("llm.prompt") {
		t.Fatal("the fixture needs the configuration to turn the tap on")
	}

	// The operator stops it, the way the command does.
	if err := SetLogTap(control, "llm.prompt", TapOff); err != nil {
		t.Fatal(err)
	}
	applyLogTaps(cfg, control)
	if logsink.TapEnabled("llm.prompt") {
		t.Error("A STOPPED TAP IS STILL RECORDING: the configuration asks for it and the stop did not override it")
	}

	// The readback says so, and says which layer is speaking.
	ctl, err := ReadLogControl(control)
	if err != nil {
		t.Fatal(err)
	}
	if got := DescribeLogging(cfg, ctl, control); !strings.Contains(got, "STOPPED by the overlay") {
		t.Errorf("the readback does not report the stop it was asked for:\n%s", got)
	}

	// And clear hands it back, which is a different act.
	if err := SetLogTap(control, "llm.prompt", TapClear); err != nil {
		t.Fatal(err)
	}
	applyLogTaps(cfg, control)
	if !logsink.TapEnabled("llm.prompt") {
		t.Error("clear did not hand the question back to the configuration")
	}
}

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

	// A level that parses, then a detail that does not.
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

	// An unreadable overlay must restore the configuration's groups
	// rather than leaving the previous overlay's standing.
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
