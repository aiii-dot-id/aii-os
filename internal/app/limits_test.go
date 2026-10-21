package app

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/limits"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
)

// configWith is the default configuration with a block of overrides read as
// a file states it.
func configWith(t *testing.T, overrides string) Config {
	t.Helper()
	cfg := readConfigFile(t, `{"overrides":`+overrides+`}`)
	return *cfg
}

// loadRefusal is the loader's refusal of a file with this block.
func loadRefusal(t *testing.T, overrides string) error {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"overrides":`+overrides+`}`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := ReadConfig(path)
	return err
}

// MODEL BEATS PROVIDER BEATS MAIN, PER KEY. For
// every key an override may set: the model's value where
// its model runs, the provider's for its other models, the main setting for
// every other provider, the default where the main setting is not set; and
// each says where it came from. A provider-only key is the provider's for
// every model it serves.
func TestModelBeatsProviderBeatsMainPerKey(t *testing.T) {
	for _, s := range cognitionSettings {
		if s.perModel == "" {
			continue
		}
		t.Run(s.key, func(t *testing.T) {
			at := func(k int) limits.Ceiling { return limits.Of(max(s.min, 1) + k) }
			section, key, _ := strings.Cut(s.key, ".")
			block := map[string]any{"P": map[string]any{section: map[string]any{key: at(2)}}}
			if s.perModel == "model" {
				block["P"].(map[string]any)["models"] = map[string]any{"m": map[string]any{section: map[string]any{key: at(3)}}}
			}
			raw, _ := json.Marshal(block)
			cfg := configWith(t, string(raw))
			cfg.Agency.MaxToolRounds = limits.Unlimited // no clamp or grace rule is between the levels here
			if s.key == maxToolRoundsSetting.key {
				cfg.Agency.MaxToolRounds = limits.Ceiling{}
			}
			main := cfg
			s.set(&main, at(1))
			check := func(c Config, entry, model string, want limits.Ceiling, level string) {
				t.Helper()
				l := limitsFor(c, providerEntry{Name: entry}, model)
				if got := l.Value(s); got != want || l.Source[s.key].Level != level {
					t.Errorf("%s · %s: %s in force from %q; want %s from %q", entry, model, got, l.Source[s.key].Level, want, level)
				}
			}
			if s.perModel == "model" {
				check(main, "P", "m", at(3), "model")
			} else {
				check(main, "P", "m", at(2), "provider")
			}
			check(main, "P", "other", at(2), "provider")
			check(main, "Q", "m", at(1), "main")
			check(cfg, "Q", "m", s.value(cfg), map[bool]string{true: "default", false: "main"}[s.get(&cfg).IsZero()])
		})
	}
}

// AN OVERRIDE NAMES ONLY WHAT IT CHANGES: every other key of the profile is
// the main setting's, from the main setting or the default.
func TestAnOverrideNamesOnlyWhatItChanges(t *testing.T) {
	cfg := configWith(t, `{"Local server":{"agency":{"turn_token_budget":"unlimited"}}}`)
	l := limitsFor(cfg, providerEntry{Name: "Local server"}, "any")
	for _, s := range cognitionSettings {
		if s.key == turnTokenBudgetSetting.key {
			if l.Value(s) != limits.Unlimited || l.Source[s.key].Level != "provider" {
				t.Errorf("the override's own key is %s from %q", l.Value(s), l.Source[s.key].Level)
			}
			continue
		}
		if got, want := l.Value(s), s.value(cfg); got != want || (l.Source[s.key].Level != "main" && l.Source[s.key].Level != "default") {
			t.Errorf("%s: %s from %q under an override that does not name it; want the main settings' %s", s.key, got, l.Source[s.key].Level, want)
		}
	}
	if len(l.Held) != 0 {
		t.Errorf("a profile inside its bounds held %v", l.Held)
	}
}

// A GLOBAL KEY IN AN OVERRIDE IS REFUSED BY NAME: it bounds the machine or
// the rhythm, never a model. The local wall names its per-provider
// counterpart. A key the table does not know, a value of the
// wrong kind and "unlimited" for a key that is never lifted are refused by
// their path too.
func TestAGlobalOnlyKeyInAnOverrideIsRefusedByName(t *testing.T) {
	for block, want := range map[string]string{
		`{"P":{"agency":{"max_parallel_subagents":2}}}`:                        "overrides.P.agency.max_parallel_subagents: agency.max_parallel_subagents is global",
		`{"P":{"agency":{"tool_schema_refusals":2}}}`:                          "agency.tool_schema_refusals is global",
		`{"P":{"agency":{"rhythm_seconds":60}}}`:                               "agency.rhythm_seconds is global",
		`{"P":{"agency":{"subagent_wall_seconds_local":60}}}`:                  "set agency.subagent_wall_seconds on the local provider instead",
		`{"P":{"agency":{"no_such_key":1}}}`:                                   "overrides.P.agency.no_such_key: no such setting",
		`{"P":{"agency":{"subagent_wall_seconds":"unlimited"}}}`:               "unlimited is not a value it takes",
		`{"P":{"agency":{"turn_token_budget":"lots"}}}`:                        "overrides.P.agency.turn_token_budget",
		`{"P":{"memory":{"backfill_rows":1}}}`:                                 "overrides.P.memory: not a section",
		`{"P":{"models":{"m":{"models":{}}}}}`:                                 "overrides.P.models.m.models",
		`{"P":{"models":{"m":{"agency":{"subagent_max_mints":3}}}}}`:           "overrides.P.models.m.agency.subagent_max_mints",
		`{"Local server":{"models":{"q/3.6":{"agency":{"queue_workers":9}}}}}`: "overrides.Local server.models.q/3.6.agency.queue_workers",
	} {
		err := loadRefusal(t, block)
		var refusal *CognitionRefusal
		if !errors.As(err, &refusal) || !strings.Contains(err.Error(), want) {
			t.Errorf("%s loaded, or was refused without %q: %v", block, want, err)
		}
	}
}

// A PROVIDER'S KEY UNDER A MODEL IS REFUSED BY NAME: the transport's keys
// describe the server, and a model of it has the server's.
func TestAProviderOnlyKeyUnderAModelIsRefusedByName(t *testing.T) {
	err := loadRefusal(t, `{"Anthropic":{"models":{"claude-opus-5":{"llm":{"timeout_seconds":600}}}}}`)
	if err == nil || !strings.Contains(err.Error(), "overrides.Anthropic.models.claude-opus-5.llm.timeout_seconds") || !strings.Contains(err.Error(), "describes the server: set it on the provider") {
		t.Fatalf("a server's key under a model was not refused by its path: %v", err)
	}
	cfg := configWith(t, `{"Anthropic":{"llm":{"timeout_seconds":600}}}`)
	if l := limitsFor(cfg, providerEntry{Name: "Anthropic"}, "claude-opus-5"); l.N(timeoutSetting) != 600 {
		t.Fatalf("control: the provider's timeout is not its models': %d", l.N(timeoutSetting))
	}
}

// AN OVERRIDE FOR A MISSING PROVIDER IS NAMED, NOT REFUSED: the file
// loads, the block is kept and written back, and the path is named as
// unused; so is a model the entry's own list does not name. A model an
// entry only discovers is not called unused.
func TestAnOverrideForAMissingProviderIsNamedNotRefused(t *testing.T) {
	cfg := configWith(t, `{"Gone":{"agency":{"max_tool_rounds":5}},"Listed":{"models":{"m-1":{"agency":{"max_tool_rounds":6}},"m-9":{"agency":{"max_tool_rounds":7}}}},"Discovers":{"models":{"x":{"agency":{"max_tool_rounds":8}}}}}`)
	reg := &providerRegistry{Providers: []providerEntry{{Name: "Listed", Models: []string{"m-1"}}, {Name: "Discovers"}}}
	if got, want := strings.Join(unusedOverrides(cfg.Overrides, reg), " "), "overrides.Gone overrides.Listed.models.m-9"; got != want {
		t.Errorf("unused overrides: %q; want %q", got, want)
	}
	written, err := json.Marshal(cfg)
	if err != nil || !strings.Contains(string(written), `"Gone":{"agency":{"max_tool_rounds":5}}`) {
		t.Fatalf("an unused override was not kept: %s", written)
	}
}

// THE CHILD-ROUNDS CLAMP HOLDS INSIDE EACH PROFILE: a child's rounds are at
// most the resident's of the same profile, which may be another provider's
// than the main settings'. It narrows and never widens, and unlimited is at
// least any number.
func TestTheChildRoundsClampHoldsInsideEachProfile(t *testing.T) {
	cfg := configWith(t, `{"Small":{"agency":{"max_tool_rounds":10,"subagent_max_tool_rounds":20}},"Free":{"agency":{"max_tool_rounds":"unlimited","subagent_max_tool_rounds":"unlimited"}}}`)
	if l := limitsFor(cfg, providerEntry{Name: "Small"}, ""); l.Value(subRoundsSetting) != limits.Of(10) {
		t.Errorf("a child's 20 rounds under the provider's 10 are %s", l.Value(subRoundsSetting))
	}
	if l := limitsFor(cfg, providerEntry{Name: "Free"}, ""); l.Value(subRoundsSetting) != limits.Unlimited {
		t.Errorf("a child lifted under a resident lifted is %s", l.Value(subRoundsSetting))
	}
	if l := limitsFor(cfg, providerEntry{Name: "Other"}, ""); l.Value(subRoundsSetting) != limits.Of(30) {
		t.Errorf("another provider's child is %s; want the main settings' 30", l.Value(subRoundsSetting))
	}
}

// THE GRACE IS HELD INSIDE EACH PROFILE: a checkpoint's grace the operator
// set that leaves no round under a profile's round ceiling is held there to
// the most that does, and said with the profile it holds for, at load and
// in the log. (A grace left at its default is never asked, as in the main
// settings: graceFits.)
func TestTheGraceIsHeldInsideEachProfile(t *testing.T) {
	logs := logsink.CaptureForTest(t)
	cfg := *readConfigFile(t, `{"agency":{"turn_checkpoint_grace_rounds":4},"overrides":{"Tiny":{"agency":{"max_tool_rounds":4}}}}`)
	l := limitsFor(cfg, providerEntry{Name: "Tiny"}, "")
	if got := l.N(graceRoundsSetting); got != 2 {
		t.Errorf("a grace of 4 under the provider's 4 rounds is %d in force; want 2, the most that leaves a round", got)
	}
	path := "overrides.Tiny.agency.turn_checkpoint_grace_rounds"
	said := cfg.cognitionHeld[path]
	if !strings.Contains(said, "for Tiny") || !strings.Contains(said, "a grace of 2 is in force") || !strings.Contains(logs.String(), said) {
		t.Errorf("the hold is not said with its profile at load: %q\n%s", said, logs.String())
	}
	if other := limitsFor(cfg, providerEntry{Name: "Other"}, ""); other.N(graceRoundsSetting) != 4 {
		t.Errorf("another provider's grace moved: %d", other.N(graceRoundsSetting))
	}
}

// AN OVERRIDE'S VALUE OUTSIDE ITS BOUNDS IS HELD AND SAID, never refused,
// by its path, and the file is left as it is.
func TestAnOverrideOutsideItsBoundsIsHeldAndSaid(t *testing.T) {
	cfg := configWith(t, `{"P":{"llm":{"timeout_seconds":100000}},"Q":{"models":{"m":{"agency":{"turn_checkpoint_slack":5000}}}}}`)
	if l := limitsFor(cfg, providerEntry{Name: "P"}, ""); l.N(timeoutSetting) != 86_400 {
		t.Errorf("a provider's timeout over its most is %d in force", l.N(timeoutSetting))
	}
	if l := limitsFor(cfg, providerEntry{Name: "Q"}, "m"); l.N(slackSetting) != 1000 {
		t.Errorf("a model's slack over its most is %d in force", l.N(slackSetting))
	}
	for _, path := range []string{"overrides.P.llm.timeout_seconds", "overrides.Q.models.m.agency.turn_checkpoint_slack"} {
		if !strings.Contains(cfg.cognitionHeld[path], path+" ") || !strings.Contains(cfg.cognitionHeld[path], "is in force") {
			t.Errorf("%s: not said: %q", path, cfg.cognitionHeld[path])
		}
	}
	if raw, _ := json.Marshal(cfg.Overrides); !strings.Contains(string(raw), `"timeout_seconds":100000`) {
		t.Errorf("the block was rewritten: %s", raw)
	}
}

// THE LOCAL WALL IS THE MAIN SETTING FOR A LOCAL ENTRY: a child on
// a local entry starts from agency.subagent_wall_seconds_local, and a wall
// the local provider's override sets is its own.
func TestTheLocalWallIsTheMainSettingForALocalEntry(t *testing.T) {
	cfg := configWith(t, `{"Local server":{"models":{"big":{"agency":{"subagent_wall_seconds":7200}}}}}`)
	local := providerEntry{Name: "Local server", Local: true}
	if l := limitsFor(cfg, local, "small"); l.N(subWallSetting) != 1800 || l.Source[subWallSetting.key].Level != "main" {
		t.Errorf("a local child's wall is %d from %q; want the local wall, 1800, the main setting's", l.N(subWallSetting), l.Source[subWallSetting.key].Level)
	}
	if l := limitsFor(cfg, local, "big"); l.N(subWallSetting) != 7200 {
		t.Errorf("a local model's own wall is %d", l.N(subWallSetting))
	}
	if l := limitsFor(cfg, providerEntry{Name: "Hosted"}, "big"); l.N(subWallSetting) != 1200 {
		t.Errorf("a hosted child's wall is %d; want the main wall, 1200", l.N(subWallSetting))
	}
}

// A GEMINI ENTRY TAKES THE BLOCK: an override is keyed by the entry's name,
// whatever dialect the entry speaks.
func TestAGeminiEntryTakesTheBlock(t *testing.T) {
	cfg := configWith(t, `{"Gemini":{"agency":{"turn_token_budget":250000},"models":{"gemini-3-pro":{"prompt":{"max_tool_result_chars":9000}}}}}`)
	l := limitsFor(cfg, providerEntry{Name: "Gemini", APIType: "gemini"}, "gemini-3-pro")
	if l.Value(turnTokenBudgetSetting) != limits.Of(250_000) || l.N(toolResultCharsSetting) != 9000 {
		t.Fatalf("a Gemini entry's overrides: fence %s, result chars %d", l.Value(turnTokenBudgetSetting), l.N(toolResultCharsSetting))
	}
}

// NAMES WITH SPACES AND DOTS, IDS WITH SLASHES AND DOTS, ARE KEYS: the block
// is addressed by its own maps, never by a dotted path.
func TestOverrideKeysMayHoldSpacesDotsAndSlashes(t *testing.T) {
	cfg := configWith(t, `{"My server v1.2":{"models":{"org/qwen3.6-27b":{"agency":{"max_tool_rounds":7}}}}}`)
	if l := limitsFor(cfg, providerEntry{Name: "My server v1.2"}, "org/qwen3.6-27b"); l.Value(maxToolRoundsSetting) != limits.Of(7) {
		t.Fatalf("a model id with a slash and dots under a name with spaces and dots: %s", l.Value(maxToolRoundsSetting))
	}
}
