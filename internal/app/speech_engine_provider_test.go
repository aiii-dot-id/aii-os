package app

import (
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/pluginfacility"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost/pluginhosttest"
)

// engineOnThisMachine gives the application an ACTIVE speech engine, the
// way an activation does: a live session driver on a plugin with an id and
// a title. Settings → Speech offers it from here (speech_engines.go).
func engineOnThisMachine(t *testing.T, a *App, id, title string) {
	t.Helper()
	c := pluginhosttest.SessionClientFrames(make(chan []byte, 4), io.Discard, 8)
	v := pluginhost.NewVoiceSession(c)
	a.pluginMu.Lock()
	a.plugins = append(a.plugins, pluginWithTestVoice(&pluginhost.ActivePlugin{ID: id, Title: title}, v))
	a.pluginMu.Unlock()
	if !a.VoiceEngine() {
		t.Fatal("fixture: the engine is not active")
	}
}

func TestTheInstalledEngineCanBeChosenInSettingsSpeech(t *testing.T) {
	dir := t.TempDir()
	a := New(&Config{SourcePath: filepath.Join(dir, "config.json")})
	engineOnThisMachine(t, a, "id.aiii.voice", "AII Voice")

	cfg := a.configSnapshot()
	cfg.Speech.STT.Provider = "id.aiii.voice"
	cfg.Speech.TTS.Provider = "id.aiii.voice"
	// An EMPTY registry, which is the truth: no service answers for a
	// plugin id, and none is asked to.
	if err := a.checkSpeech(&cfg, &providerRegistry{}, true, true); err != nil {
		t.Fatalf("the installed engine must be choosable for both directions: %v", err)
	}

	// A NAME THAT IS NOT THE ENGINE STILL HAS TO RESOLVE. The refusal the
	// operator can act on — a typo, a service not added — is unchanged.
	other := a.configSnapshot()
	other.Speech.TTS.Provider = "Deepgram"
	err := a.checkSpeech(&other, &providerRegistry{}, false, true)
	if err == nil || !strings.Contains(err.Error(), "not in providers.json") {
		t.Fatalf("a service that is not there is still refused: %v", err)
	}
	// And with no engine installed, the plugin id is exactly such a name.
	bare := New(&Config{SourcePath: filepath.Join(dir, "config.json")})
	cfg2 := bare.configSnapshot()
	cfg2.Speech.TTS.Provider = "id.aiii.voice"
	if err := bare.checkSpeech(&cfg2, &providerRegistry{}, false, true); err == nil {
		t.Fatal("with no engine installed its id resolves to nothing and must be refused")
	}
}

// THE ENGINE IS NOT A CLOUD CLIENT. A pointer at it resolves to no
// transcription client, no synthesizer and no reply voice: the engine
// hears and speaks inside its session, and a page told a service speaks
// would read every reply aloud in the browser's own voice beside it.
func TestTheInstalledEngineResolvesToNoServiceAndNoReplyVoice(t *testing.T) {
	dir := t.TempDir()
	a := New(&Config{SourcePath: filepath.Join(dir, "config.json")})
	engineOnThisMachine(t, a, "id.aiii.voice", "AII Voice")
	a.cfgMu.Lock()
	a.cfg.Speech.STT.Provider = "id.aiii.voice"
	a.cfg.Speech.TTS.Provider = "id.aiii.voice"
	a.cfgMu.Unlock()

	if c, err := a.resolveSpeech(); err != nil || c != nil {
		t.Fatalf("hearing: client=%v err=%v — the engine is not reached over the network", c != nil, err)
	}
	if s, err := a.resolveTTS(); err != nil || s != nil {
		t.Fatalf("speaking: synthesizer=%v err=%v", s != nil, err)
	}
	if v := a.replyVoice(); v != "" {
		t.Fatalf("the engine speaks in its session; no service voice is named: %q", v)
	}

	// A CLOUD SERVICE NAMED BESIDE IT IS STILL A SERVICE: the engine does
	// not swallow the operator's other choice.
	a.cfgMu.Lock()
	a.cfg.Speech.TTS.Provider = "ElevenLabs"
	a.cfgMu.Unlock()
	if v := a.replyVoice(); v != "ElevenLabs" {
		t.Fatalf("a service the operator named must still speak replies: %q", v)
	}
}

// engineInstalledHere gives the application a plugin that is INSTALLED and
// not running: the facility wants it and knows the family its package
// declared, and no activation serves it — the minutes an engine spends
// warming up, fetching its models for the first time, or backing off after
// a crash. The facility is never attached, so nothing starts.
func engineInstalledHere(t *testing.T, a *App, id, family string) *pluginfacility.Facility {
	t.Helper()
	f := pluginfacility.New(pluginfacility.Config{})
	t.Cleanup(f.Close)
	a.facilityOnce.Do(func() { a.facility = f })
	if a.pluginFacility() != f {
		t.Fatal("fixture: the application already built its own facility")
	}
	f.Observe([]pluginfacility.Observed{installedPackage(id, family)}, pluginfacility.Policy{Revision: 1})
	if a.VoiceEngine() {
		t.Fatal("fixture: an engine is running")
	}
	return f
}

func installedPackage(id, family string) pluginfacility.Observed {
	return pluginfacility.Observed{ID: id, Package: id + ".aiiospkg", Hash: "sha256:" + id, Family: family}
}

// SPEECH SETTINGS ARE SAVED WHILE THE ENGINE IS NOT RUNNING (audit H7).
// A Speech card sends its whole direction on every Save — the engine's id
// with the ceiling or a tunable beside it — and the save refused all of it
// while the engine was warming up, fetching its models or coming back from
// a crash:
//
//	voice input refused: speech.stt.provider "id.aiii.voice" is not in
//	providers.json
//
// because only a RUNNING engine was known. An installed one is the same
// engine: there is still no endpoint to reach and no key to send, and a
// providers.json that does not even parse is still not consulted.
func TestSpeechSettingsAreSavedWhileTheEngineIsNotRunning(t *testing.T) {
	a := newVoiceApp(t)
	engineInstalledHere(t, a, "id.aiii.voice", "voice_interface")
	if err := os.WriteFile(a.providersPath(), []byte(`{"providers":[`), 0o600); err != nil {
		t.Fatal(err)
	}
	a.cfg.Speech.STT.Provider = "id.aiii.voice"
	a.cfg.Speech.TTS.Provider = "id.aiii.voice"
	// Each card as the page sends it: the direction whole, and its ceiling.
	if _, err := a.applyConfigChange(map[string]any{
		"speech.stt.provider": "id.aiii.voice", "speech.stt.model": "", "speech.stt.language": "",
		"speech.stt.monthly_minutes": 30,
	}); err != nil {
		t.Fatalf("the hearing card was refused while its engine is not running: %v", err)
	}
	if _, err := a.applyConfigChange(map[string]any{
		"speech.tts.provider": "id.aiii.voice", "speech.tts.model": "", "speech.tts.voice": "",
		"speech.tts.monthly_characters": 9000,
	}); err != nil {
		t.Fatalf("the speaking card was refused while its engine is not running: %v", err)
	}
	sp := a.configSnapshot().Speech
	if sp.STT.Provider != "id.aiii.voice" || sp.STT.MonthlyMinutes != 30 || sp.TTS.Provider != "id.aiii.voice" || sp.TTS.MonthlyCharacters != 9000 {
		t.Fatalf("what was saved is not in force: %+v", sp)
	}
	// Choosing it anew is the same answer: it need not be running to be
	// the engine a direction points at.
	a.cfg.Speech.STT = STTConfig{}
	if _, err := a.applyConfigChange(map[string]any{"speech.stt.provider": "id.aiii.voice"}); err != nil {
		t.Fatalf("the installed engine could not be chosen while it is not running: %v", err)
	}
	// And the check itself, asked directly, has nothing to ask it.
	cfg := a.configSnapshot()
	if err := a.checkSpeech(&cfg, &providerRegistry{}, true, true); err != nil {
		t.Fatalf("the check looked an installed engine up as a service: %v", err)
	}
}

// AND ONLY AN ENGINE IS ONE. A name nothing installed answers to, an
// installed plugin that is not a speech engine, and an engine whose package
// has left, still have to resolve in providers.json, and are refused —
// keeping what was in force — when they do not.
func TestANameNoInstalledEngineAnswersToIsStillRefused(t *testing.T) {
	a := newVoiceApp(t)
	f := engineInstalledHere(t, a, "id.example.gone", "voice_interface")
	// Its package left the directory: the facility no longer wants it.
	f.Observe([]pluginfacility.Observed{installedPackage("id.example.tools", "tool_bridge")}, pluginfacility.Policy{Revision: 1})
	if err := os.WriteFile(a.providersPath(), []byte(`{"providers":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	before := a.configSnapshot().Speech
	for _, name := range []string{"id.nowhere.voice", "id.example.tools", "id.example.gone"} {
		for _, key := range []string{"speech.stt.provider", "speech.tts.provider"} {
			_, err := a.applyConfigChange(map[string]any{key: name})
			if err == nil || !strings.Contains(err.Error(), "not in providers.json") {
				t.Errorf("%s = %q was not refused as a name nothing answers to: %v", key, name, err)
			}
		}
	}
	if !reflect.DeepEqual(a.configSnapshot().Speech, before) {
		t.Fatal("a refused save changed what is in force")
	}
}
