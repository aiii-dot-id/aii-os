package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/cognitive"
	"github.com/aiii-dot-id/aii-os/internal/dashboard"
)

// readBack is a configuration state read at a dotted key, as the Settings
// page reads a save back (configValue in views/settings.js): the JSON the
// page receives, walked by the key's parts. Absent is nil.
func readBack(t *testing.T, st *dashboard.ConfigState, key string) any {
	t.Helper()
	raw, err := json.Marshal(st)
	if err != nil {
		t.Fatal(err)
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	for _, part := range strings.Split(key, ".") {
		m, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		v = m[part]
	}
	return v
}

var cognitionDefaults = map[string]int{
	"agency.rhythm_seconds":                   600,
	"cognition.consolidate_spacing_seconds":   1800,
	"cognition.consolidate_batch":             30,
	"cognition.consolidate_max_ops":           32,
	"agency.outcome_window":                   512,
	"cognition.self_model_every_minutes":      250,
	"cognition.identity_review_every_minutes": 500,
	"cognition.self_model_evidence_rows":      8,
	"cognition.self_model_spoken_rows":        8,
	"prompt.self_model_max_tokens":            2000,
	"prompt.ring3_max_chars":                  8000,
	"prompt.surfacing_max_chars":              2400,
	"prompt.dream_conversation_max_chars":     12000,
	"prompt.tensions_max_chars":               4000,
	"prompt.brief_max_chars":                  1200,
	"prompt.alarm_firings":                    20,
	"prompt.excerpt_chars":                    240,
	"prompt.ring3_recent_experiences":         5,
	"agency.turn_token_budget":                600000,
	"agency.tool_schema_refusals":             3,
	"agency.turn_checkpoint_calls":            32,
	"agency.turn_checkpoint_grace_rounds":     4,
	"agency.turn_checkpoint_slack":            2,
	"agency.max_turn_continuations":           8,
	"agency.continuation_wall_seconds":        2700,
	"agency.alarm_wall_seconds":               900,
	"agency.harvest_wall_seconds":             900,
}

func TestEachCognitionSettingRoundTripsFromTheFileThroughTheDoorAndIsRefusedOutsideItsBounds(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	// Defaults: nothing written but the cadence, at sixty seconds and at
	// the seeded six hundred.
	if seeded := defaultConfig().Agency.RhythmSeconds; seeded != 600 {
		t.Fatalf("rig: the seeded cadence is %d", seeded)
	}
	for _, cadence := range []int{60, 600} {
		fresh := defaultConfig()
		fresh.SourcePath = path
		fresh.Agency.RhythmSeconds = cadence
		state := New(fresh).configState()
		seen := map[string]bool{}
		for _, s := range state.Cognition.Settings {
			if s.Card != "" {
				continue // drawn by its own card (limits.go), not the Cognition card
			}
			seen[s.Key] = true
			want, ok := cognitionDefaults[s.Key]
			if !ok {
				t.Errorf("the card offers %s, which this test does not know", s.Key)
				continue
			}
			if s.Default != want {
				t.Errorf("at a %d-second cadence %s defaults to %d; contract states %d", cadence, s.Key, s.Default, want)
			}
			if s.Min > want || s.Max < want {
				t.Errorf("%s: its default %d is outside its own bounds %d–%d", s.Key, want, s.Min, s.Max)
			}
			if key := s.Key; key != "agency.rhythm_seconds" && cognitionValue(*fresh, key) != want {
				t.Errorf("at a %d-second cadence %s in force is %d with nothing set; want its default, %d", cadence, key, cognitionValue(*fresh, key), want)
			}
		}
		for key := range cognitionDefaults {
			if !seen[key] {
				t.Errorf("%s is not on the Cognition card", key)
			}
		}
		if state.Cognition.MorningBriefDefault != "07:00" {
			t.Errorf("the brief's hour defaults to %q; the code carried 07:00", state.Cognition.MorningBriefDefault)
		}
		if got := rhythmSpacing(*fresh); got != 30*time.Minute {
			t.Errorf("at a %d-second cadence consolidation's spacing in force is %s; the code carried thirty minutes, and a faster cadence buys no more passes", cadence, got)
		}
		selfModel := reflectionPulses(cognitionValue(*fresh, "cognition.self_model_every_minutes"), pulseSeconds(*fresh))
		review := reflectionPulses(cognitionValue(*fresh, "cognition.identity_review_every_minutes"), pulseSeconds(*fresh))
		if selfModel != 50 || review != 100 {
			t.Errorf("at a %d-second cadence the portrait and the review come every %d and %d pulses; contract ruled 50 and 100", cadence, selfModel, review)
		}
	}

	// config.json → read-back: a value of each, as an operator writes it.
	written := map[string]int{}
	file := map[string]map[string]any{"prompt": {"recent_turns": 20}, "agency": {}, "cognition": {"morning_brief_at": "06:45"}}
	for key, def := range cognitionDefaults {
		v := def + 1
		if key == "cognition.consolidate_spacing_seconds" {
			v = 2400 // longer than the cadence written beside it
		}
		written[key] = v
		parts := strings.SplitN(key, ".", 2)
		file[parts[0]][parts[1]] = v
	}
	raw, _ := json.Marshal(file)
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := ReadConfig(path)
	if err != nil {
		t.Fatalf("a file with every rhythm setting inside its bounds: %v", err)
	}
	a := New(cfg)
	st := a.configState()
	for key, want := range written {
		if got := readBack(t, st, key); fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("%s written %d in config.json reads back %v", key, want, got)
		}
	}
	if got := readBack(t, st, briefAtKey); got != "06:45" {
		t.Errorf("the brief's hour written 06:45 reads back %v", got)
	}

	// door → file → read-back, live.
	change := map[string]any{briefAtKey: "off"}
	for key, def := range cognitionDefaults {
		change[key] = float64(def + 2) // as JSON numbers arrive from the page
	}
	change["cognition.consolidate_spacing_seconds"] = float64(3000)
	st, err = a.applyConfigChange(change)
	if err != nil {
		t.Fatalf("a change of every rhythm setting inside its bounds was refused: %v", err)
	}
	if len(st.RestartRequired) != 0 {
		t.Errorf("the rhythm's settings apply at the next pass; the door asked for a restart of %v", st.RestartRequired)
	}
	onDisk, err := ReadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	for key, sent := range change {
		if got := readBack(t, st, key); fmt.Sprint(got) != fmt.Sprint(sent) {
			t.Errorf("%s sent %v reads back %v", key, sent, got)
		}
		if key == briefAtKey {
			if onDisk.Cognition.MorningBriefAt != "off" {
				t.Errorf("the brief's hour was not written to the file: %q", onDisk.Cognition.MorningBriefAt)
			}
			continue
		}
		s, _ := cognitionSettingFor(key)
		if got := s.get(onDisk); fmt.Sprint(got) != fmt.Sprint(sent) {
			t.Errorf("%s sent %v is %s in the file", key, sent, got)
		}
	}

	// Out of bounds: refused with the bounds, nothing changed.
	before := a.configSnapshot()
	for _, s := range cognitionSettings {
		bads := []int{s.min - 1, s.max + 1}
		if s.floor != "" {
			bads = append(bads, max(1, s.def(before).N/100)) // the default with two digits dropped
		}
		for _, bad := range bads {
			if bad == 0 && s.zero {
				continue // zero is the default, not out of bounds
			}
			_, err := a.applyConfigChange(map[string]any{s.key: float64(bad)})
			bounds := fmt.Sprintf("between %d and %d", s.min, s.max)
			var refusal *CognitionRefusal
			if !errors.As(err, &refusal) || refusal.Key != s.key || !strings.Contains(refusal.Requirement, bounds) {
				t.Errorf("%s = %d: the door must refuse it, typed, naming the key and its bounds (%s): %v", s.key, bad, bounds, err)
				continue
			}
			if bad < s.min && !strings.Contains(refusal.Requirement, s.floor) {
				t.Errorf("%s = %d: the refusal does not say why %d is the floor (%q): %v", s.key, bad, s.min, s.floor, err)
			}
		}
	}
	var refusal *CognitionRefusal
	for _, bad := range []any{"7 am", "25:00", 7} {
		if _, err := a.applyConfigChange(map[string]any{briefAtKey: bad}); !errors.As(err, &refusal) || refusal.Key != briefAtKey || !strings.Contains(refusal.Requirement, "HH:MM") {
			t.Errorf("the brief's hour %v must be refused by its type, the requirement naming its form: %v", bad, err)
		}
	}
	if _, err := a.applyConfigChange(map[string]any{"cognition.consolidate_spacing_seconds": float64(before.Agency.RhythmSeconds)}); !errors.As(err, &refusal) ||
		refusal.Key != spacingSetting.key || !strings.Contains(refusal.Requirement, "agency.rhythm_seconds") || !strings.Contains(refusal.Requirement, "DREAM") {
		t.Errorf("a spacing no longer than the cadence must be refused, typed, naming both and why: %v", err)
	}
	after := a.configSnapshot()
	for _, s := range cognitionSettings {
		if s.get(&after) != s.get(&before) {
			t.Errorf("%s moved from %s to %s across refused changes", s.key, s.get(&before), s.get(&after))
		}
	}
	if after.Cognition.MorningBriefAt != before.Cognition.MorningBriefAt {
		t.Errorf("the brief's hour moved across refused changes: %q", after.Cognition.MorningBriefAt)
	}

	// Null clears a setting whose zero is the default, and the spacing then
	// follows the cadence again.
	st, err = a.applyConfigChange(map[string]any{"cognition.consolidate_spacing_seconds": nil, "agency.rhythm_seconds": float64(1800)})
	if err != nil {
		t.Fatal(err)
	}
	if got := readBack(t, st, "cognition.consolidate_spacing_seconds"); got != nil {
		t.Errorf("a cleared spacing reads back %v; absent is the default", got)
	}
	for _, s := range st.Cognition.Settings {
		if s.Key == "cognition.consolidate_spacing_seconds" && s.Default != 5400 {
			t.Errorf("with the cadence at 1800 the spacing's default is %d; it follows the cadence, three ticks (5400)", s.Default)
		}
	}
}

func TestASaveThatSetsNoRhythmKeyWritesNoCognitionBlock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"prompt":{"recent_turns":20}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := ReadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	a := New(cfg)
	saved := func(change map[string]any) map[string]json.RawMessage {
		t.Helper()
		if _, err := a.applyConfigChange(change); err != nil {
			t.Fatal(err)
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var file map[string]json.RawMessage
		if err := json.Unmarshal(raw, &file); err != nil {
			t.Fatal(err)
		}
		return file
	}
	if block, written := saved(map[string]any{"prompt.ring3_max_chars": float64(9000)})["cognition"]; written {
		t.Fatalf("a save of another setting wrote a cognition block an older binary refuses: %s", block)
	}
	if block := saved(map[string]any{"cognition.consolidate_batch": float64(20)})["cognition"]; string(block) == "" || !strings.Contains(string(block), `"consolidate_batch": 20`) {
		t.Fatalf("a rhythm key that is set is written under cognition: %q", block)
	}
}

func TestTheRhythmsSettingsApplyAtOnceOnALiveIdentity(t *testing.T) {
	dir := t.TempDir()
	keyPath, ledgerPath, dbPath := birthFixture(t, dir, "Rhythm")
	a := New(safebootConfig(t, dir, "Rhythm", keyPath, ledgerPath, dbPath))
	if err := startLiveForTest(a); err != nil {
		t.Fatalf("startLive: %v", err)
	}
	t.Cleanup(func() { stopApp(t, a) })
	set := a.cognition.rhythm.Load()
	if set == nil {
		t.Fatal("the live boot did not publish the rhythm's facilities")
	}
	if got := set.rhythm.Spacing(); got != 30*time.Minute {
		t.Fatalf("rig: the boot's spacing is %s", got)
	}
	alarm := func(clock, id string) (deadline, repeat int64, ok bool) {
		t.Helper()
		rows, err := a.store.DueAlarms(clock, math.MaxInt64, 100)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range rows {
			if r.AlarmID == id {
				if r.RepeatEvery != nil {
					repeat = *r.RepeatEvery
				}
				return r.Deadline, repeat, true
			}
		}
		return 0, 0, false
	}
	if _, every, ok := alarm("life", cognitive.ReflectSelfModelAlarm); !ok || every != cognitive.SelfModelCadence {
		t.Fatalf("rig: the portrait's alarm repeats every %d pulses (armed %v)", every, ok)
	}

	if _, err := a.applyConfigChange(map[string]any{
		"cognition.consolidate_spacing_seconds": float64(2400),
		"prompt.self_model_max_tokens":          float64(1500),
		"cognition.self_model_every_minutes":    float64(100),
		briefAtKey:                              "off",
	}); err != nil {
		t.Fatal(err)
	}
	if got := set.rhythm.Spacing(); got != 40*time.Minute {
		t.Errorf("the spacing in force is %s; the door set 2400 seconds", got)
	}
	if got := a.store.SelfModelMaxTokens(); got != 1500 {
		t.Errorf("the portrait's budget in force is %d; the door set 1500", got)
	}
	if _, every, _ := alarm("life", cognitive.ReflectSelfModelAlarm); every != 20 {
		t.Errorf("the portrait's alarm repeats every %d pulses; 100 lived minutes at a 300-second pulse is 20", every)
	}
	// Off, the brief's alarm stands, held out of the clock's reach: its row
	// is what holds the name "morning_brief" against a timer of the
	// identity's own, and a firing that was already under way is accepted
	// and holds it again. (That such a firing writes no brief is held where
	// a model answers: internal/cognitive, and brief_alarm_owed_test.go.)
	if deadline, _, ok := alarm("wall", "morning_brief"); !ok || time.Until(time.UnixMilli(deadline)) < 50*365*24*time.Hour {
		t.Errorf("the brief was turned off: its alarm's row there %v, due %s; want it there and out of the clock's reach", ok, time.UnixMilli(deadline).UTC().Format("2006-01-02"))
	}
	if res := set.brief.OnAlarm(context.Background(), "morning_brief", "wall", 0, ""); !res.Accepted || res.NextDeadline == nil || time.Until(time.UnixMilli(*res.NextDeadline)) < 50*365*24*time.Hour {
		t.Errorf("with the brief off a firing was: accepted %v, held again %v; want it accepted and its alarm held far ahead", res.Accepted, res.NextDeadline != nil)
	}

	if _, err := a.applyConfigChange(map[string]any{briefAtKey: "08:30"}); err != nil {
		t.Fatal(err)
	}
	deadline, _, ok := alarm("wall", "morning_brief")
	if !ok {
		t.Fatal("the brief was turned back on at 08:30 and no alarm is armed")
	}
	if at := time.UnixMilli(deadline).In(time.UTC); at.Hour() != 8 || at.Minute() != 30 {
		t.Errorf("the brief's alarm is set for %s; the door set 08:30 (UTC, no zone configured)", at.Format("15:04"))
	}
}
