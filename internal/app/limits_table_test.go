package app

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/limits"
)

// doorInteger is the settings door's own reading of a number from the page
// (a JSON number arrives as a float64).
func doorInteger(k string, v any) (int, error) {
	if f, ok := v.(float64); ok && f == float64(int(f)) {
		return int(f), nil
	}
	return 0, errors.New(k + ": want a number")
}

// "UNLIMITED" IS ACCEPTED ONLY WHERE THE TABLE ALLOWS IT. A
// spend limit may be lifted, at the door and
// in the file; a wall, a count of closing rounds, a slack or a bound on what
// is read may not, and is refused by its key, at the door in the door's
// words and in the file before it loads. Every key that may be lifted is a
// per-model key, and no wall is.
func TestUnlimitedIsAcceptedOnlyWhereTheTableAllowsIt(t *testing.T) {
	var lifted []string
	for _, s := range cognitionSettings {
		if s.unlimited {
			lifted = append(lifted, s.key)
			if s.perModel != "model" {
				t.Errorf("%s may be lifted and is not a per-model key (%q)", s.key, s.perModel)
			}
			if strings.Contains(s.key, "wall") {
				t.Errorf("%s is a wall and may be lifted", s.key)
			}
		}
		c := *defaultConfig()
		n, err := doorValue(s, c, limits.Word, doorInteger)
		var refusal *CognitionRefusal
		switch {
		case s.unlimited && (err != nil || n != limits.Unlimited):
			t.Errorf("%s: the door did not take %q: %v", s.key, limits.Word, err)
		case !s.unlimited && (!errors.As(err, &refusal) || refusal.Key != s.key || !strings.Contains(err.Error(), "unlimited is not a value it takes")):
			t.Errorf("%s: the door took %q, or refused it in other words: %v", s.key, limits.Word, err)
		}
	}
	want := "agency.turn_token_budget agency.turn_checkpoint_calls agency.max_turn_continuations agency.max_tool_rounds agency.max_tool_calls agency.subagent_max_tool_rounds agency.subagent_max_tool_calls agency.subagent_max_legs"
	if got := strings.Join(lifted, " "); got != want {
		t.Errorf("the keys that may be lifted are %s; want %s", got, want)
	}

	cfg := readConfigFile(t, `{"agency":{"turn_token_budget":"unlimited","max_tool_rounds":"unlimited","subagent_max_tool_calls":"unlimited"}}`)
	if cfg.Agency.TurnTokenBudget != limits.Unlimited || cfg.Agency.MaxToolRounds != limits.Unlimited || cfg.Agency.SubagentMaxToolCalls != limits.Unlimited {
		t.Fatalf("a file's lifted limits did not load as lifted: %+v", cfg.Agency)
	}
	if cfg.Agency.SubagentMaxToolRounds != limits.Of(limits.DefaultSubRounds) {
		t.Errorf("a child's rounds under unlimited resident rounds are %s; want the default, which no limit is below", cfg.Agency.SubagentMaxToolRounds)
	}
	written, err := json.Marshal(cfg.Agency)
	if err != nil || !strings.Contains(string(written), `"turn_token_budget":"unlimited"`) {
		t.Fatalf("a lifted limit is not written back as %q: %s", limits.Word, written)
	}
	for _, body := range []string{
		`{"agency":{"turn_checkpoint_slack":"unlimited"}}`,
		`{"agency":{"subagent_wall_seconds":"unlimited"}}`,
		`{"agency":{"turn_token_budget":"Unlimited"}}`,
		`{"llm":{"timeout_seconds":"unlimited"}}`,
	} {
		path := filepath.Join(t.TempDir(), "config.json")
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadConfig(path); err == nil {
			t.Errorf("%s loaded", body)
		}
	}
}

// ZERO STILL TAKES THE DEFAULT AND NULL STILL CLEARS. A key whose zero was
// "the default" still reads so, in the file and at the door; null at the
// door clears what was set; and a key whose zero the loader refused still
// refuses it.
func TestZeroStillTakesTheDefaultAndNullStillClears(t *testing.T) {
	cfg := readConfigFile(t, `{"agency":{"turn_token_budget":0,"subagent_max_tool_calls":0,"turn_checkpoint_calls":0},"prompt":{"max_tool_result_chars":0}}`)
	for _, s := range []cognitionSetting{turnTokenBudgetSetting, subCallsSetting, checkpointCallsSetting, toolResultCharsSetting} {
		if got := s.value(*cfg); got != s.def(*cfg) || got.IsZero() {
			t.Errorf("%s 0 in the file is %s in force; want its default %s", s.key, got, s.def(*cfg))
		}
	}
	c := *defaultConfig()
	if handled, err := applyCognitionChange(&c, "agency.turn_token_budget", limits.Word, doorInteger); !handled || err != nil || c.Agency.TurnTokenBudget != limits.Unlimited {
		t.Fatalf("the door did not lift the fence: %v", err)
	}
	if handled, err := applyCognitionChange(&c, "agency.turn_token_budget", nil, doorInteger); !handled || err != nil || !c.Agency.TurnTokenBudget.IsZero() || turnTokenBudgetSetting.value(c) != limits.Of(limits.DefaultTurnTokens) {
		t.Fatalf("null did not clear the lifted fence to the default: %v, %s", err, c.Agency.TurnTokenBudget)
	}
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"agency":{"max_tool_rounds":0}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadConfig(path); err == nil || !strings.Contains(err.Error(), "agency.max_tool_rounds") {
		t.Errorf("a round ceiling of zero, which the loader always refused, loaded: %v", err)
	}
}

// A FILE THE LOADER TOOK BEFORE STILL LOADS, AND IS WRITTEN BACK AS IT WAS.
// The configuration the released 0.1.17 program writes for a first install
// loads, and saving it gives the same bytes: the limits that became
// liftable are written as the numbers they were, and nothing new appears.
func TestAFileTheLoaderTookBeforeStillLoads(t *testing.T) {
	path := filepath.Join("testdata", "identity-by-v0.1.17", "config.json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := ReadConfig(path)
	if err != nil {
		t.Fatalf("the 0.1.17 configuration does not load: %v", err)
	}
	after, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Errorf("the 0.1.17 configuration is written back changed:\n%s", lineDiff(string(before), string(after)))
	}
}

// lineDiff names the first lines two texts differ at.
func lineDiff(a, b string) string {
	x, y := strings.Split(a, "\n"), strings.Split(b, "\n")
	var out []string
	for i := 0; i < max(len(x), len(y)) && len(out) < 12; i++ {
		var l, r string
		if i < len(x) {
			l = x[i]
		}
		if i < len(y) {
			r = y[i]
		}
		if l != r {
			out = append(out, "- "+l, "+ "+r)
		}
	}
	return strings.Join(out, "\n")
}
