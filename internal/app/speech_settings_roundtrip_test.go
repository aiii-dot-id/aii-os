package app

import (
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"testing"
)

func TestSpeechSettingsSaveReloadAndRefusalAreWhole(t *testing.T) {
	a := newVoiceApp(t)
	applyDefaults(a.cfg)
	a.cfg.Prompt.RecentTurns = 20
	changes := map[string]any{
		"speech.speakers":               map[string]any{"mode": "only", "uids": []string{"speaker-z", "speaker-a"}},
		"speech.mode":                   map[string]any{"listen": "interactive", "speak": ""},
		"speech.stt.monthly_minutes":    30,
		"speech.tts.monthly_characters": 1000,
	}
	state, err := a.applyConfigChange(changes)
	if err != nil {
		t.Fatal(err)
	}
	if state.Speech.Mode.Speak != "auto" || !reflect.DeepEqual(state.Speech.Speakers.UIDs, []string{"speaker-a", "speaker-z"}) {
		t.Fatalf("wire readback: %+v %+v", state.Speech.Mode, state.Speech.Speakers)
	}
	path := a.configSnapshot().SourcePath
	loaded, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Speech.Mode.Speak != "" || loaded.Speech.Speakers.Mode != "only" || loaded.Speech.STT.MonthlyMinutes != 30 || loaded.Speech.TTS.MonthlyCharacters != 1000 {
		t.Fatal("saved speech values did not survive reload")
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	inMemory, _ := json.Marshal(a.configSnapshot().Speech)
	changes["speech.mode"] = map[string]any{"listen": "off", "speak": "off"}
	changes["speech.speakers"] = map[string]any{"mode": "all", "uids": []string{}}
	_, err = a.applyConfigChangeWith(changes, func(*Config) (bool, error) { return false, errors.New("fixture disk refusal") })
	if err == nil {
		t.Fatal("failed persistence acknowledged")
	}
	after, _ := os.ReadFile(path)
	current, _ := json.Marshal(a.configSnapshot().Speech)
	if string(before) != string(after) || string(inMemory) != string(current) {
		t.Fatal("failed speech save partially changed disk or runtime")
	}
}
