package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
	"github.com/aiii-dot-id/aii-os/internal/tools"
)

func TestHowAPluginsChildIsKeptReachesThePluginHostFromTheDoor(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	cfg := defaultConfig()
	cfg.SourcePath = path
	cfg.Identity.LedgerPath = filepath.Join(t.TempDir(), "identity.ledger")
	if _, err := saveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	a := New(cfg)
	opts, err := a.buildPluginOptions(newAdmissionStore(t, t.TempDir()), tools.NewRegistry(t.TempDir(), nil, tools.Timeouts{}), nil)
	if err != nil {
		t.Fatal(err)
	}
	a.pluginOpts = opts
	defaults := pluginhost.ChildBounds{Stop: 5 * time.Second, HandOver: 30 * time.Second, SessionControl: 5 * time.Second, RestartAttempts: 5}
	if got := a.pluginOptions().Bounds; got != defaults {
		t.Fatalf("with none set the options hold %+v, want the numbers the code carried %+v", got, defaults)
	}
	revision := a.pluginPolicy(a.configSnapshot(), 0, false).Revision

	if _, err := a.applyConfigChange(map[string]any{
		stopSetting.key: 45000.0, handOverSetting.key: 120000.0, sessionControlSetting.key: 9000.0, restartAttemptsSetting.key: 12.0,
	}); err != nil {
		t.Fatalf("the door refused the four: %v", err)
	}
	want := pluginhost.ChildBounds{Stop: 45 * time.Second, HandOver: 2 * time.Minute, SessionControl: 9 * time.Second, RestartAttempts: 12}
	if got := a.pluginOptions().Bounds; got != want {
		t.Errorf("the options every activation reads hold %+v, want the operator's %+v", got, want)
	}
	if pol := a.pluginPolicy(a.configSnapshot(), 0, false); pol.RestartAttempts != 12 || pol.Revision == revision {
		t.Errorf("the facility's policy holds %d attempts in revision %d (was %d); want the operator's 12 in a new revision", pol.RestartAttempts, pol.Revision, revision)
	}
	if got, want := newPluginRuntime(a).RetireBound(), (pluginhost.ChildBounds{Stop: 45 * time.Second}).Retirement(); got != want || got <= 45*time.Second {
		t.Errorf("a stop of 45s: a retirement allows %s, want %s (the stop and the kill's reap)", got, want)
	}
	if got := a.engineJoinBound(); got != 45*time.Second {
		t.Errorf("a stop of 45s: the teardown joins engine work for %s", got)
	}
	if got := a.childBounds().HandOverBudget(); got != 2*time.Minute {
		t.Errorf("the hand-over of open sessions waits %s, want the operator's 2m0s", got)
	}

	if _, err := a.applyConfigChange(map[string]any{stopSetting.key: nil, handOverSetting.key: nil, sessionControlSetting.key: nil, restartAttemptsSetting.key: nil}); err != nil {
		t.Fatalf("null was refused: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"stop_ms", "hand_over_ms", "session_control_ms", "restart_attempts"} {
		if strings.Contains(string(raw), `"`+key+`"`) {
			t.Errorf("cleared, %s is still in the file:\n%s", key, raw)
		}
	}
	if got := a.pluginOptions().Bounds; got != defaults {
		t.Errorf("cleared, the options hold %+v, want the defaults %+v", got, defaults)
	}
}
