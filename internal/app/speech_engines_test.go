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

// AN ENGINE INSTALLED ON THIS MACHINE IS OFFERED WHERE ENGINES ARE
// CHOSEN. Settings → Speech listed the cloud services and nothing else,
// so the engine that was actually doing the listening could not be seen
// there — it simply won the microphone while its tuning sat on another
// page, and choosing a service beside it was not expressible.
func TestSettingsSpeechOffersAnInstalledEngine(t *testing.T) {
	st := speechState(Config{}, nil, nil, []dashboard.SpeechService{testEngine()})
	if len(st.Services) != 1 || !st.Services[0].Plugin || st.Services[0].Name != "id.test.voice" {
		t.Fatalf("the installed engine must be offered: %+v", st.Services)
	}
	// It is offered under its plugin id, because that is what
	// speech.stt.provider holds — and read under its title.
	if st.Services[0].Title != "Test Voice" {
		t.Fatalf("the operator reads a title, the config holds an id: %+v", st.Services[0])
	}
	// Nothing chosen: it serves, and says it serves because it is here.
	if !st.STT.Plugin || !st.STT.Default || st.STT.Provider != "id.test.voice" {
		t.Fatalf("with nothing chosen the installed engine serves: %+v", st.STT)
	}
	if !st.TTS.Plugin || !st.TTS.Default {
		t.Fatalf("both halves, not just hearing: %+v", st.TTS)
	}
	// There is nothing to reach and no key to show.
	if st.STT.Endpoint != "" || st.STT.APIKeyMasked != "" || st.STT.Error != "" {
		t.Fatalf("an engine on this machine has no endpoint and no key: %+v", st.STT)
	}
}

// NAMED, IT SERVES AND SAYS SO; A SERVICE NAMED BESIDE IT WINS.
func TestAnInstalledEngineIsTheDefaultAndNotAnOverride(t *testing.T) {
	engines := []dashboard.SpeechService{testEngine()}
	named := Config{Speech: SpeechConfig{STT: STTConfig{Provider: "id.test.voice"}}}
	st := speechState(named, nil, nil, engines)
	if !st.STT.Plugin || st.STT.Default {
		t.Fatalf("naming it is the same answer said out loud, not the default: %+v", st.STT)
	}
	// A service named beside it is NOT served by the engine. With no
	// registry it cannot resolve, which is the honest answer here: what
	// matters is that the engine did not quietly take it over.
	other := Config{Speech: SpeechConfig{STT: STTConfig{Provider: "Deepgram"}}}
	st = speechState(other, nil, nil, engines)
	if st.STT.Plugin || st.STT.Provider != "Deepgram" {
		t.Fatalf("a service the operator named must not be overridden by an installed engine: %+v", st.STT)
	}
	// And the engine is still OFFERED, so they can choose it back.
	if len(st.Services) == 0 || !st.Services[0].Plugin {
		t.Fatalf("the engine stays on offer whatever is chosen: %+v", st.Services)
	}
}

// AN ENGINE HERE NEEDS NOTHING FROM providers.json — and is still
// offered when that file will not load, which is exactly when an
// operator most wants the engine that does not depend on it.
func TestAnInstalledEngineSurvivesABrokenProvidersFile(t *testing.T) {
	st := speechState(Config{}, nil, errBrokenProviders, []dashboard.SpeechService{testEngine()})
	if len(st.Services) != 1 || !st.Services[0].Plugin {
		t.Fatalf("the engine must still be offered: %+v", st.Services)
	}
	if !st.STT.Plugin || st.STT.Error != "" {
		t.Fatalf("and it resolves without that file: %+v", st.STT)
	}
}

// ONLY WHAT THE PACKAGE SCOPED TO A HALF OF SPEECH TRAVELS THERE. A
// setting it declared as neither is not about hearing or speaking, and
// stays on its own card — the operator keeps one page for the plugin
// and one for the speech it does.
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

// AN ENGINE IS OFFERED IN ADDITION TO EVERY SERVICE, NEVER INSTEAD OF
// THEM. With an engine installed and nothing chosen, the engine serves
// both halves and the readback returns before any pointer is resolved —
// and the line that built the offer list sat below that return, so both
// pickers showed the engine and nothing else on exactly the machines
// where an engine was working (found by the operator, 0.1.7). Every
// earlier test here passed a nil registry, which offers nothing, so the
// tests agreed with the defect. This one passes a real registry.
func TestAnInstalledEngineDoesNotEmptyThePickers(t *testing.T) {
	reg := &providerRegistry{} // providers.json loaded, none of the operator's own in it: the shipped services
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
