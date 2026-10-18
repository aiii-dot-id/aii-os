package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/logsink"
)

var cognitionBounds = map[string][2]int{
	"agency.rhythm_seconds":                   {1, 86_400},
	"cognition.consolidate_spacing_seconds":   {2, 259_200},
	"cognition.consolidate_batch":             {1, 200},
	"cognition.consolidate_max_ops":           {2, 512},
	"agency.outcome_window":                   {1, 1_000_000},
	"cognition.self_model_every_minutes":      {1, 1_000_000},
	"cognition.identity_review_every_minutes": {1, 1_000_000},
	"cognition.self_model_evidence_rows":      {1, 200},
	"cognition.self_model_spoken_rows":        {1, 200},
	"prompt.self_model_max_tokens":            {200, 1_000_000},
	"prompt.ring3_max_chars":                  {600, 10_000_000},
	"prompt.surfacing_max_chars":              {600, 10_000_000},
	"prompt.dream_conversation_max_chars":     {1, 100_000_000},
	"prompt.tensions_max_chars":               {1, 10_000_000},
	"prompt.brief_max_chars":                  {600, 1_000_000},
	"prompt.alarm_firings":                    {1, 100_000},
	"prompt.excerpt_chars":                    {1, 100_000},
	"prompt.ring3_recent_experiences":         {1, 1_000},
	"agency.turn_token_budget":                {1, 1_000_000_000},
	"agency.tool_schema_refusals":             {1, 32},
	"agency.turn_checkpoint_calls":            {1, 1_000_000_000},
	"agency.turn_checkpoint_grace_rounds":     {1, 1_000_000},
	"agency.turn_checkpoint_slack":            {1, 1_000},
	"agency.max_turn_continuations":           {1, 1_000_000},
	"agency.continuation_wall_seconds":        {1, 86_400},
	"agency.alarm_wall_seconds":               {1, 86_400},
	"agency.harvest_wall_seconds":             {1, 86_400},
}

func TestTheRhythmsBoundsAreTheOnesTheRulingStates(t *testing.T) {
	seen := map[string]bool{}
	for _, s := range cognitionSettings {
		seen[s.key] = true
		want, ok := cognitionBounds[s.key]
		if !ok {
			t.Errorf("%s is in the table and this test does not know its bounds", s.key)
			continue
		}
		if s.min != want[0] || s.max != want[1] {
			t.Errorf("%s is held between %d and %d; contract states %d and %d", s.key, s.min, s.max, want[0], want[1])
		}
		if (s.floor != "") != (s.min > 1 && s.key != spacingSetting.key) {
			t.Errorf("%s: a floor above one says why, and no other does (min %d, reason %q)", s.key, s.min, s.floor)
		}
	}
	for key := range cognitionBounds {
		if !seen[key] {
			t.Errorf("%s has bounds here and is not in the table", key)
		}
	}
}

// fileWith is a config.json that states one rhythm setting and nothing
// else of the rhythm's.
func fileWith(t *testing.T, key string, v any) string {
	t.Helper()
	parts := strings.SplitN(key, ".", 2)
	raw, err := json.Marshal(map[string]map[string]any{"prompt": {"recent_turns": 20}, parts[0]: {parts[1]: v}})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestAValueOutsideItsBoundsLoadsHeldToThemAndSaidAndTheDoorStillRefusesIt(t *testing.T) {
	for _, s := range cognitionSettings {
		lo, hi, dflt := cognitionBounds[s.key][0], cognitionBounds[s.key][1], cognitionDefaults[s.key]
		spacing := s.key == spacingSetting.key
		// what the file states, and what is then in force
		cases := map[int]int{hi + 1: hi}
		if lo > 1 {
			cases[lo-1] = lo
		}
		if typo := dflt / 100; typo > 0 && typo < lo {
			cases[typo] = lo
		}
		if s.key == "agency.turn_token_budget" {
			cases[-1] = dflt
		}
		if spacing {
			cases[lo-1], cases[600] = dflt, dflt // at the seeded 600-second cadence a tick meets both
		}
		if s.key == graceRoundsSetting.key {
			// Under the seeded thirty rounds a grace of twenty-nine, and one
			// over the most the row takes, leave no round for a checkpoint:
			// each is held to the most that does, twenty-eight.
			cases = map[int]int{hi + 1: 28, 29: 28}
		}
		for stated, force := range cases {
			path := fileWith(t, s.key, stated)
			before, _ := os.ReadFile(path)
			logs := logsink.CaptureForTest(t)
			cfg, err := ReadConfig(path)
			if err != nil {
				t.Errorf("%s = %d: A BOUND STOPPED THE LOAD: %v", s.key, stated, err)
				continue
			}
			if got := cognitionValue(*cfg, s.key); got != force {
				t.Errorf("%s = %d in the file: %d is in force; want %d", s.key, stated, got, force)
			}
			if err := validCognition(*cfg); err != nil {
				t.Errorf("%s = %d: what the loader holds is not what the door would take: %v", s.key, stated, err)
			}
			said := cfg.cognitionHeld[s.key]
			for _, want := range []string{s.key + " " + fmt.Sprint(stated) + " is ", fmt.Sprint(force), "is in force", "the file is left as it is", "config.json", "Settings → Identity → Cognition"} {
				if !strings.Contains(said, want) {
					t.Errorf("%s = %d: the loader's sentence does not say %q: %q", s.key, stated, want, said)
				}
			}
			if stated > 0 && stated < lo && !spacing && !strings.Contains(said, s.floor) {
				t.Errorf("%s = %d: the sentence does not say why %d is the floor (%q): %q", s.key, stated, lo, s.floor, said)
			}
			if said == "" || !strings.Contains(logs.String(), said) || !strings.Contains(logs.String(), "config.decision") {
				t.Errorf("%s = %d: the log does not carry the loader's sentence %q:\n%s", s.key, stated, said, logs.String())
			}
			a := New(cfg)
			for _, on := range a.configState().Cognition.Settings {
				if on.Key == s.key && on.Held != said {
					t.Errorf("%s = %d: the card is not told the value in force is not the file's: %q", s.key, stated, on.Held)
				}
			}
			if after, _ := os.ReadFile(path); string(after) != string(before) {
				t.Errorf("%s = %d: the loader rewrote the file", s.key, stated)
			}
			var refusal *CognitionRefusal
			if _, err := a.applyConfigChange(map[string]any{s.key: float64(stated)}); !errors.As(err, &refusal) || refusal.Key != s.key {
				t.Errorf("%s = %d typed at the settings door must be refused, by its type: %v", s.key, stated, err)
			}
			if after, _ := os.ReadFile(path); string(after) != string(before) {
				t.Errorf("%s = %d: a refused change wrote the file", s.key, stated)
			}
		}
	}

	// A value inside its bounds is read without a word.
	quiet := logsink.CaptureForTest(t)
	if cfg, err := ReadConfig(fileWith(t, "prompt.surfacing_max_chars", 600)); err != nil || cfg.cognitionHeld != nil || strings.Contains(quiet.String(), "config.decision") {
		t.Errorf("a value at its floor was held or said: %v %v\n%s", err, cfg.cognitionHeld, quiet.String())
	}

	// What no bound stands in for is refused, as the loader always refused
	// it: a value below zero and the two zeros, by their type; a value of
	// the wrong JSON type; a key this build does not know.
	for _, s := range cognitionSettings {
		for _, stated := range []int{0, -1} {
			if stated == 0 && s.zero || stated < 0 && s.key == "agency.turn_token_budget" {
				continue
			}
			var refusal *CognitionRefusal
			if _, err := ReadConfig(fileWith(t, s.key, stated)); !errors.As(err, &refusal) || refusal.Key != s.key {
				t.Errorf("%s = %d in the file must be refused at load, typed: %v", s.key, stated, err)
			}
		}
	}
	for key, v := range map[string]any{"prompt.surfacing_max_chars": "2400", "prompt.surfacing_max_characters": 2400, "cognition.morning_brief_at": "25:00"} {
		if _, err := ReadConfig(fileWith(t, key, v)); err == nil {
			t.Errorf("%s = %v in the file must be refused at load", key, v)
		}
	}
}

func TestTheBootLoadsAConfigWhoseNoteBoundLostTwoDigits(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"prompt":{"recent_turns":20,"surfacing_max_chars":24}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("THE IDENTITY DOES NOT START: %v", err)
	}
	if got := dreamConfig(*cfg).MaxChars; got != 600 {
		t.Fatalf("DREAM's note bound in force is %d; the floor, 600, stands in for the file's 24", got)
	}
}

func TestAReloadTakesAFileWithAValueOutsideItsBoundsAndTheCardSaysSoUntilTheFileIsSaved(t *testing.T) {
	dir := t.TempDir()
	cfg := defaultConfig()
	cfg.SourcePath = filepath.Join(dir, "config.json")
	if _, err := saveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	a := New(cfg)
	a.live.Store(true)
	held := func() string {
		for _, s := range a.configState().Cognition.Settings {
			if s.Key == "prompt.surfacing_max_chars" {
				return s.Held
			}
		}
		t.Fatal("the note's bound is not on the card")
		return ""
	}
	edit := func(prompt, cognition string) {
		t.Helper()
		raw, err := os.ReadFile(cfg.SourcePath)
		if err != nil {
			t.Fatal(err)
		}
		var file map[string]json.RawMessage
		if err := json.Unmarshal(raw, &file); err != nil {
			t.Fatal(err)
		}
		file["prompt"], file["cognition"] = json.RawMessage(prompt), json.RawMessage(cognition)
		if raw, err = json.Marshal(file); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(cfg.SourcePath, raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	edit(`{"recent_turns":20,"surfacing_max_chars":24}`, `{"consolidate_batch":7}`)
	logs := logsink.CaptureForTest(t)
	a.reloadConfig()
	now := a.configSnapshot()
	if now.Cognition.ConsolidateBatch != 7 {
		t.Fatalf("THE RELOAD IGNORED THE FILE: the batch in force is %d; the file states 7\n%s", now.Cognition.ConsolidateBatch, logs.String())
	}
	if now.Prompt.SurfacingMaxChars != 600 || !strings.Contains(held(), "prompt.surfacing_max_chars 24 is under") || !strings.Contains(logs.String(), held()) {
		t.Fatalf("the note's bound is held at its floor and said: %d in force, the card says %q\n%s", now.Prompt.SurfacingMaxChars, held(), logs.String())
	}

	// The file put right by hand, then wrong again where the value in
	// force does not move: the card follows the file both times.
	edit(`{"recent_turns":20,"surfacing_max_chars":600}`, `{"consolidate_batch":7}`)
	a.reloadConfig()
	if said := held(); said != "" {
		t.Fatalf("the file states a value inside its bounds and the card still says %q", said)
	}
	edit(`{"recent_turns":20,"surfacing_max_chars":24}`, `{"consolidate_batch":7}`)
	logs = logsink.CaptureForTest(t)
	a.reloadConfig()
	if !strings.Contains(held(), "24 is under") {
		t.Fatalf("the file states 24 again, 600 is still in force, and the card says %q", held())
	}
	if strings.Contains(logs.String(), "SAVED FOR NEXT BOOT") {
		t.Fatalf("what the loader held is in force now, and the reload said it waits for the next boot:\n%s", logs.String())
	}

	// A save through the door writes what is in force: the file no longer
	// states 24, and neither does the card, before or after the reload
	// that follows every save.
	if _, err := a.applyConfigChange(map[string]any{"cognition.consolidate_batch": float64(9)}); err != nil {
		t.Fatal(err)
	}
	onDisk, err := ReadConfig(cfg.SourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if onDisk.Prompt.SurfacingMaxChars != 600 || onDisk.cognitionHeld != nil || held() != "" {
		t.Fatalf("after a save the file states %d (held: %v) and the card says %q", onDisk.Prompt.SurfacingMaxChars, onDisk.cognitionHeld, held())
	}
	a.reloadConfig()
	if held() != "" {
		t.Fatalf("the reload of the host's own save brought the sentence back: %q", held())
	}
}

func TestARhythmSettingOfTheWrongKindIsRefusedByItsType(t *testing.T) {
	cfg := defaultConfig()
	cfg.SourcePath = filepath.Join(t.TempDir(), "config.json")
	a := New(cfg)
	for key, bad := range map[string]any{"cognition.consolidate_batch": "twelve", "prompt.ring3_max_chars": 1.5, "agency.rhythm_seconds": true} {
		var refusal *CognitionRefusal
		if _, err := a.applyConfigChange(map[string]any{key: bad}); !errors.As(err, &refusal) || refusal.Key != key || refusal.Requirement == "" {
			t.Errorf("%s = %v must be refused by its type, naming the key and what is wanted: %v", key, bad, err)
		}
	}
}
