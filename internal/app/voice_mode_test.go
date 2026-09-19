package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// .
// .
// .
// .

func TestAnUnsetVoiceModeIsAutoAndMeansToday(t *testing.T) {
	listen, speak, set := resolveVoiceMode(VoiceModeConfig{})
	if listen != "interactive" || speak != "auto" || set {
		t.Fatalf("auto resolved to (%s, %s, set=%v), want (interactive, auto, false)", listen, speak, set)
	}
	if got := voiceModeName(listen, speak); got != "interactive" {
		t.Fatalf("auto is named %q, want interactive", got)
	}
	for _, tc := range []struct{ listen, speak, name string }{
		{"off", "on", "earbuds"}, {"meeting", "off", "meeting"}, {"off", "off", "off"},
		{"meeting", "on", "meeting, spoken replies"}, {"interactive", "off", "interactive, text replies"},
	} {
		if got := voiceModeName(tc.listen, tc.speak); got != tc.name {
			t.Errorf("(%s, %s) named %q, want %q", tc.listen, tc.speak, got, tc.name)
		}
	}
	s := voiceModeSentence("meeting", "off")
	for _, want := range []string{"meeting (listen: meeting, speak: off)", "not addressed to you", "replies are text"} {
		if !strings.Contains(s, want) {
			t.Errorf("the meeting sentence omits %q: %q", want, s)
		}
	}
}

func TestLoadConfigRefusesAVoiceModeOutsideItsSet(t *testing.T) {
	for _, body := range []string{
		`{"prompt":{"recent_turns":20},"speech":{"mode":{"listen":"meating"}}}`,
		`{"prompt":{"recent_turns":20},"speech":{"mode":{"speak":"loud"}}}`,
	} {
		path := filepath.Join(t.TempDir(), "config.json")
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadConfig(path); err == nil || !strings.Contains(err.Error(), "speech.mode.") {
			t.Fatalf("got %v, want a speech.mode refusal for %s", err, body)
		}
	}
	// .
	// .
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"prompt":{"recent_turns":20},"speech":{"mode":{"listen":"meeting","speak":"off"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Speech.Mode.Listen != "meeting" || cfg.Speech.Mode.Speak != "off" {
		t.Fatalf("a valid mode did not load: %+v", cfg.Speech.Mode)
	}
}

func TestAVoiceModeIsAcceptedWholeOrNotAtAll(t *testing.T) {
	a := newVoiceApp(t)
	persist := func(*Config) (bool, error) { return true, nil }
	for _, bad := range []interface{}{
		map[string]any{"listen": "meating"},
		map[string]any{"speak": "loud"},
		map[string]any{"listen": "meeting", "loud": true},
		map[string]any{"listen": 1},
		"meeting",
	} {
		if _, err := a.applyConfigChangeWith(map[string]interface{}{"speech.mode": bad}, persist); err == nil {
			t.Errorf("accepted %v", bad)
		}
		if got := a.configSnapshot().Speech.Mode; got != (VoiceModeConfig{}) {
			t.Fatalf("a refusal changed the mode: %+v", got)
		}
	}
	if _, err := a.applyConfigChangeWith(map[string]interface{}{"speech.mode": map[string]any{"listen": " Meeting ", "speak": "off"}}, persist); err != nil {
		t.Fatal(err)
	}
	got := a.configSnapshot().Speech.Mode
	if got.Listen != "meeting" || got.Speak != "off" || got.Revision != 1 {
		t.Fatalf("the accepted mode is not what was sent: %+v", got)
	}
	// .
	if l, s, set := a.voiceMode(); l != "meeting" || s != "off" || !set {
		t.Fatalf("voiceMode() = (%s, %s, %v)", l, s, set)
	}
	if l, s, rev := a.VoiceMode(); l != "meeting" || s != "off" || rev != 1 {
		t.Fatalf("VoiceMode() = (%s, %s, %d)", l, s, rev)
	}
	if st := a.voiceModeState(); st.Name != "meeting" || st.Revision != 1 || !st.Set {
		t.Fatalf("readback: %+v", st)
	}
	// .
	// .
	// .
	if _, err := a.applyConfigChangeWith(map[string]interface{}{"speech.mode": map[string]any{"speak": "on"}}, persist); err != nil {
		t.Fatal(err)
	}
	if got := a.configSnapshot().Speech.Mode; got.Listen != "meeting" || got.Speak != "on" || got.Revision != 2 {
		t.Fatalf("the second change did not keep the half it did not name: %+v", got)
	}
	if l, s, _ := a.voiceMode(); l != "meeting" || s != "on" {
		t.Fatalf("resolved (%s, %s), want (meeting, on)", l, s)
	}
	// .
	// .
	if _, err := a.applyConfigChangeWith(map[string]interface{}{"speech.mode": map[string]any{"listen": "", "speak": "on"}}, persist); err != nil {
		t.Fatal(err)
	}
	if got := a.configSnapshot().Speech.Mode; got.Listen != "" || got.Speak != "on" || got.Revision != 3 {
		t.Fatalf("an explicit empty half was not stored as auto: %+v", got)
	}
	if l, s, _ := a.voiceMode(); l != "interactive" || s != "on" {
		t.Fatalf("resolved (%s, %s), want (interactive, on)", l, s)
	}
	if got := a.configSnapshot().Speech.Speakers; got.Revision != 0 {
		t.Fatalf("the speaker policy moved with the mode: %+v", got)
	}
}
