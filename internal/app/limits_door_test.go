package app

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/limits"
)

// settingState is the read-back's row for a key.
func settingState(t *testing.T, st *dashboard.ConfigState, key string) dashboard.CognitionSettingState {
	t.Helper()
	for _, s := range st.Cognition.Settings {
		if s.Key == key {
			return s
		}
	}
	t.Fatalf("the read-back has no row %s", key)
	return dashboard.CognitionSettingState{}
}

// asPage is a block as the page sends it: decoded JSON.
func asPage(t *testing.T, block string) any {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(block), &v); err != nil {
		t.Fatal(err)
	}
	return v
}

// AN OVERRIDE ROUND-TRIPS FROM THE FILE THROUGH THE DOOR: the read-back
// states each key's overrides and the block's digest; a save of the whole
// block with that digest is written to config.json and read back, applies
// to the identity's next turn, and null clears it.
func TestAnOverrideRoundTripsFromTheFileThroughTheDoor(t *testing.T) {
	app, _ := dispatchApp(t)
	defer stopApp(t, app)
	st := app.configState()
	if row := settingState(t, st, "agency.turn_token_budget"); len(row.Overrides) != 0 || row.Effective.Level != "main" {
		t.Fatalf("rig: %+v", row)
	}
	block := `{"test":{"agency":{"turn_token_budget":"unlimited"},"models":{"m":{"agency":{"max_tool_rounds":44}}}}}`
	st, err := app.applyConfigChange(map[string]any{"overrides": asPage(t, block), "overrides_digest": st.Cognition.OverridesDigest})
	if err != nil {
		t.Fatalf("the door refused a block inside its bounds: %v", err)
	}
	if len(st.RestartRequired) != 0 {
		t.Errorf("an override applies at the next turn; the door asked for a restart of %v", st.RestartRequired)
	}
	raw, err := os.ReadFile(app.configSnapshot().SourcePath)
	if err != nil || !strings.Contains(string(raw), `"turn_token_budget": "unlimited"`) || !strings.Contains(string(raw), `"max_tool_rounds": 44`) {
		t.Fatalf("the block was not written to config.json: %v\n%s", err, raw)
	}
	row := settingState(t, st, "agency.turn_token_budget")
	if len(row.Overrides) != 1 || row.Overrides[0] != (dashboard.LimitOverride{Provider: "test", Value: limits.Unlimited}) {
		t.Errorf("the read-back's overrides of the fence: %+v", row.Overrides)
	}
	if row.Effective != (dashboard.LimitEffective{Value: limits.Unlimited, Level: "provider", Provider: "test"}) {
		t.Errorf("the fence in force reads back %+v", row.Effective)
	}
	if got := agencyHeld(app); got.TurnTokens != limits.Unlimited || got.TurnRounds != limits.Of(44) {
		t.Errorf("the identity's next turn holds a fence of %s and %s rounds; want unlimited and the model's 44", got.TurnTokens, got.TurnRounds)
	}
	if _, err := app.applyConfigChange(map[string]any{"overrides": nil, "overrides_digest": st.Cognition.OverridesDigest}); err != nil {
		t.Fatal(err)
	}
	if got := agencyHeld(app); got.TurnTokens != limits.Of(600_000) || app.configSnapshot().Overrides != nil {
		t.Errorf("null did not clear the block: fence %s", got.TurnTokens)
	}
}

// AN OVERRIDE OUTSIDE ITS BOUNDS IS REFUSED AT THE DOOR AND HELD FROM THE
// FILE: typed, the save is refused by the value's path and
// nothing is written; stated in config.json, it loads held and said
// (TestAnOverrideOutsideItsBoundsIsHeldAndSaid). The loader's refusals are
// the door's too.
func TestAnOverrideOutsideItsBoundsIsRefusedAtTheDoorAndHeldFromTheFile(t *testing.T) {
	app, _ := dispatchApp(t)
	defer stopApp(t, app)
	digest := app.configState().Cognition.OverridesDigest
	before, _ := os.ReadFile(app.configSnapshot().SourcePath)
	for block, want := range map[string]string{
		`{"test":{"llm":{"timeout_seconds":100000}}}`:                "overrides.test.llm.timeout_seconds: 100000 is over the most it may be",
		`{"test":{"models":{"m":{"agency":{"max_tool_rounds":0}}}}}`: "",
		`{"test":{"agency":{"max_parallel_subagents":2}}}`:           "agency.max_parallel_subagents is global",
		`{"test":{"models":{"m":{"llm":{"retries":3}}}}}`:            "describes the server",
	} {
		_, err := app.applyConfigChange(map[string]any{"overrides": asPage(t, block), "overrides_digest": digest})
		var refusal *CognitionRefusal
		if want == "" {
			if err != nil {
				t.Errorf("%s: zero, which is not set, was refused: %v", block, err)
			}
			if _, err := app.applyConfigChange(map[string]any{"overrides": nil, "overrides_digest": app.configState().Cognition.OverridesDigest}); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if !errors.As(err, &refusal) || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: refused without %q: %v", block, want, err)
		}
	}
	if after, _ := os.ReadFile(app.configSnapshot().SourcePath); string(after) != string(before) {
		t.Errorf("a refused save wrote config.json")
	}
}

// A STALE OVERRIDES SAVE IS REFUSED: two pages that read the same
// block each save it whole; the second, carrying the digest of a block that
// has since moved, is refused as moved and would not undo the first.
func TestAStaleOverridesSaveIsRefused(t *testing.T) {
	app, _ := dispatchApp(t)
	defer stopApp(t, app)
	read := app.configState().Cognition.OverridesDigest
	if _, err := app.applyConfigChange(map[string]any{"overrides": asPage(t, `{"test":{"agency":{"max_tool_rounds":5}}}`), "overrides_digest": read}); err != nil {
		t.Fatal(err)
	}
	_, err := app.applyConfigChange(map[string]any{"overrides": asPage(t, `{"test":{"agency":{"max_tool_rounds":6}}}`), "overrides_digest": read})
	if !errors.Is(err, errConfigMoved) {
		t.Fatalf("a save carrying a stale digest was not refused as moved: %v", err)
	}
	if got := agencyHeld(app).TurnRounds; got != limits.Of(5) {
		t.Fatalf("the first save was undone: %s rounds", got)
	}
	if _, err := app.applyConfigChange(map[string]any{"overrides": asPage(t, `{}`)}); !errors.Is(err, errConfigMoved) {
		t.Fatalf("a save carrying no digest was taken: %v", err)
	}
	if _, err := app.applyConfigChange(map[string]any{"overrides_digest": read}); err == nil {
		t.Fatal("a digest alone was taken")
	}
}

// THE READ-BACK SAYS WHERE EACH LIMIT CAME FROM, for the identity's own
// provider and model: the model's, the provider's, the main setting's or
// the default's; each row says its card, where an override may set it and
// whether it may be lifted; unused paths are named.
func TestTheReadBackSaysWhereEachLimitCameFrom(t *testing.T) {
	app, _ := dispatchApp(t)
	defer stopApp(t, app)
	app.cfgMu.Lock()
	app.cfg.Agency.SubagentMaxLegs = limits.Of(3)
	app.cfg.Overrides = configWith(t, `{"test":{"agency":{"turn_checkpoint_calls":40},"models":{"m":{"agency":{"turn_token_budget":5000}}}},"Gone":{"agency":{"max_tool_rounds":2}}}`).Overrides
	app.cfgMu.Unlock()
	st := app.configState()
	for key, want := range map[string]dashboard.LimitEffective{
		"agency.turn_token_budget":     {Value: limits.Of(5000), Level: "model", Provider: "test", Model: "m"},
		"agency.turn_checkpoint_calls": {Value: limits.Of(40), Level: "provider", Provider: "test"},
		"agency.subagent_max_legs":     {Value: limits.Of(3), Level: "main"},
		"agency.max_tool_calls":        {Value: limits.Unlimited, Level: "default"},
	} {
		if got := settingState(t, st, key).Effective; got != want {
			t.Errorf("%s reads back %+v, want %+v", key, got, want)
		}
	}
	if row := settingState(t, st, "agency.max_tool_calls"); row.Card != "agency" || row.PerModel != "model" || !row.Unlimited || !row.DefaultUnlimited {
		t.Errorf("the call ceiling's row: %+v", row)
	}
	if row := settingState(t, st, "llm.timeout_seconds"); row.Card != "substrate" || row.PerModel != "provider" || row.Unlimited {
		t.Errorf("the timeout's row: %+v", row)
	}
	if got := strings.Join(st.Cognition.OverridesUnused, " "); got != "overrides.Gone" {
		t.Errorf("unused: %q", got)
	}
}
