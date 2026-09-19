package app

import (
	"errors"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
)

var errBrokenProviders = errors.New("providers.json: unexpected end of JSON input")

func testEngine() dashboard.SpeechService {
	return dashboard.SpeechService{Name: "id.test.voice", Title: "Test Voice", Plugin: true, Added: true,
		Speech: &dashboard.ProviderSpeech{STT: &dashboard.SpeechOffer{}, TTS: &dashboard.SpeechOffer{}}}
}

// .
// .
// .
// .
// .
func TestSettingsSpeechOffersAnInstalledEngine(t *testing.T) {
	st := speechState(Config{}, nil, nil, []dashboard.SpeechService{testEngine()})
	if len(st.Services) != 1 || !st.Services[0].Plugin || st.Services[0].Name != "id.test.voice" {
		t.Fatalf("the installed engine must be offered: %+v", st.Services)
	}
	// .
	// .
	if st.Services[0].Title != "Test Voice" {
		t.Fatalf("the operator reads a title, the config holds an id: %+v", st.Services[0])
	}
	// .
	if !st.STT.Plugin || !st.STT.Default || st.STT.Provider != "id.test.voice" {
		t.Fatalf("with nothing chosen the installed engine serves: %+v", st.STT)
	}
	if !st.TTS.Plugin || !st.TTS.Default {
		t.Fatalf("both halves, not just hearing: %+v", st.TTS)
	}
	// .
	if st.STT.Endpoint != "" || st.STT.APIKeyMasked != "" || st.STT.Error != "" {
		t.Fatalf("an engine on this machine has no endpoint and no key: %+v", st.STT)
	}
}

// .
func TestAnInstalledEngineIsTheDefaultAndNotAnOverride(t *testing.T) {
	engines := []dashboard.SpeechService{testEngine()}
	named := Config{Speech: SpeechConfig{STT: STTConfig{Provider: "id.test.voice"}}}
	st := speechState(named, nil, nil, engines)
	if !st.STT.Plugin || st.STT.Default {
		t.Fatalf("naming it is the same answer said out loud, not the default: %+v", st.STT)
	}
	// .
	// .
	// .
	other := Config{Speech: SpeechConfig{STT: STTConfig{Provider: "Deepgram"}}}
	st = speechState(other, nil, nil, engines)
	if st.STT.Plugin || st.STT.Provider != "Deepgram" {
		t.Fatalf("a service the operator named must not be overridden by an installed engine: %+v", st.STT)
	}
	// .
	if len(st.Services) == 0 || !st.Services[0].Plugin {
		t.Fatalf("the engine stays on offer whatever is chosen: %+v", st.Services)
	}
}

// .
// .
// .
func TestAnInstalledEngineSurvivesABrokenProvidersFile(t *testing.T) {
	st := speechState(Config{}, nil, errBrokenProviders, []dashboard.SpeechService{testEngine()})
	if len(st.Services) != 1 || !st.Services[0].Plugin {
		t.Fatalf("the engine must still be offered: %+v", st.Services)
	}
	if !st.STT.Plugin || st.STT.Error != "" {
		t.Fatalf("and it resolves without that file: %+v", st.STT)
	}
}

// .
// .
// .
// .
func TestOnlyScopedSettingsBelongBesideSpeech(t *testing.T) {
	a := &App{cfg: &Config{}}
	p := &pluginhost.ActivePlugin{ID: "id.test.voice", Settings: []pluginhost.SettingDecl{
		{Key: "stt_language", Type: "string", Title: "Language", Scope: pluginhost.ScopeHearing},
		{Key: "tts_voice", Type: "string", Title: "Voice", Scope: pluginhost.ScopeSpeaking},
		{Key: "session_log", Type: "boolean", Title: "Log", Scope: pluginhost.ScopeSession},
		{Key: "endpoint", Type: "string", Title: "Endpoint"},
	}}
	got := a.speechScopedSettings(p)
	if len(got) != 2 {
		t.Fatalf("only hearing and speaking belong beside Speech: %+v", got)
	}
	seen := map[string]string{}
	for _, s := range got {
		seen[s.Key] = s.Scope
	}
	if seen["stt_language"] != pluginhost.ScopeHearing || seen["tts_voice"] != pluginhost.ScopeSpeaking {
		t.Fatalf("each must carry the scope it declared: %+v", seen)
	}
	if _, ok := seen["session_log"]; ok {
		t.Fatal("a session setting is not about hearing or speaking")
	}
	if _, ok := seen["endpoint"]; ok {
		t.Fatal("a setting that scoped nothing stays on the plugin's own card")
	}
}

// .
// .
// .
// .
// .
// .
// .
// .
func TestAnInstalledEngineDoesNotEmptyThePickers(t *testing.T) {
	reg := &providerRegistry{}
	shipped := speechServices(reg)
	if len(shipped) < 2 {
		t.Fatalf("the shipped registry offers speech services to choose among: %d", len(shipped))
	}
	engines := []dashboard.SpeechService{testEngine()}
	for _, c := range []struct {
		name string
		cfg  Config
	}{
		{"nothing chosen, so the engine serves both halves", Config{}},
		{"the engine named for both halves", Config{Speech: SpeechConfig{STT: STTConfig{Provider: "id.test.voice"}, TTS: TTSConfig{Provider: "id.test.voice"}}}},
		{"a service named for one half", Config{Speech: SpeechConfig{TTS: TTSConfig{Provider: shipped[0].Name}}}},
	} {
		st := speechState(c.cfg, reg, nil, engines)
		if len(st.Services) != 1+len(shipped) || !st.Services[0].Plugin {
			names := make([]string, 0, len(st.Services))
			for _, s := range st.Services {
				names = append(names, s.Name)
			}
			t.Fatalf("%s: the pickers must offer the engine AND every service: %v", c.name, names)
		}
		for i, s := range shipped {
			if st.Services[1+i].Name != s.Name {
				t.Fatalf("%s: the services keep their order after the engine: %q at %d, want %q", c.name, st.Services[1+i].Name, 1+i, s.Name)
			}
		}
	}
}
