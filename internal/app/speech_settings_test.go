package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
)

// .
// .
// .

// .
// .
// .
func speakingVendor(t *testing.T, mode string, during func()) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var asked atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked.Add(1)
		if during != nil {
			during()
		}
		if mode == "refuse" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"detail":{"message":"invalid api key"}}`))
			return
		}
		switch {
		case r.URL.Path == "/v1/listen":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"results":{"channels":[{"alternatives":[{"transcript":""}]}]}}`))
		case strings.HasPrefix(r.URL.Path, "/v1/text-to-speech/"):
			w.Header().Set("Content-Type", "audio/pcm")
			if mode != "mute" {
				_, _ = w.Write(make([]byte, 480))
			}
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &asked
}

// .
// .
func speechSettingsApp(t *testing.T, url string) (*App, string) {
	t.Helper()
	a := newVoiceApp(t)
	path := filepath.Join(filepath.Dir(a.cfg.SourcePath), "providers.json")
	if err := os.WriteFile(path, []byte(`{"providers":[{"name":"Deepgram","url":"`+url+`","api_key":"dg-key-1234"},
		{"name":"ElevenLabs","url":"`+url+`","api_key":"el-key-1234"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	return a, path
}

func elevenLabsSpeech() map[string]interface{} {
	return map[string]interface{}{
		"speech.stt.provider": "Deepgram", "speech.stt.model": "nova-3-general", "speech.stt.language": "en",
		"speech.tts.provider": "ElevenLabs", "speech.tts.model": "eleven_flash_v2_5", "speech.tts.voice": "v1",
	}
}

// .
// .
// .
func TestSpeechSettingsApplyLive(t *testing.T) {
	vendor, asked := speakingVendor(t, "", nil)
	a, _ := speechSettingsApp(t, vendor.URL)
	if _, err := a.applyConfigChange(elevenLabsSpeech()); err != nil {
		t.Fatal(err)
	}
	sp := a.configSnapshot().Speech
	if sp.STT.Provider != "Deepgram" || sp.STT.Model != "nova-3-general" || sp.STT.Language != "en" ||
		sp.TTS.Provider != "ElevenLabs" || sp.TTS.Model != "eleven_flash_v2_5" || sp.TTS.Voice != "v1" {
		t.Fatalf("speech = %+v", sp)
	}
	if n := asked.Load(); n != 2 {
		t.Fatalf("the service was asked %d times; a save checks each direction once", n)
	}
	x, y := defaultConfig(), defaultConfig()
	y.Speech.TTS.Voice = "changed"
	blankLiveAppliable(x)
	blankLiveAppliable(y)
	if !reflect.DeepEqual(x.Speech, y.Speech) {
		t.Fatal("a speech edit would be announced as saved for next boot")
	}
}

// .
// .
// .
func TestASpeechServiceThatFailsTheCheckKeepsWhatWasInForce(t *testing.T) {
	for _, tc := range []struct {
		mode, want string
		change     map[string]interface{}
	}{
		{"refuse", "invalid api key", map[string]interface{}{"speech.stt.provider": "Deepgram", "speech.stt.model": "nova-3-general"}},
		{"refuse", "invalid api key", map[string]interface{}{"speech.tts.provider": "ElevenLabs", "speech.tts.model": "eleven_flash_v2_5", "speech.tts.voice": "v1"}},
		{"mute", "no audio", map[string]interface{}{"speech.tts.provider": "ElevenLabs", "speech.tts.model": "eleven_flash_v2_5", "speech.tts.voice": "v1"}},
	} {
		vendor, asked := speakingVendor(t, tc.mode, nil)
		a, _ := speechSettingsApp(t, vendor.URL)
		before := a.configSnapshot().Speech
		_, err := a.applyConfigChange(tc.change)
		if err == nil || !strings.Contains(err.Error(), "refused") || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s %v: saved, or refused without saying why: %v", tc.mode, tc.change, err)
		}
		if asked.Load() == 0 {
			t.Errorf("%s %v: refused without asking the service", tc.mode, tc.change)
		}
		if !reflect.DeepEqual(a.configSnapshot().Speech, before) {
			t.Errorf("%s %v: a failed check changed what is in force", tc.mode, tc.change)
		}
	}
}

// .
// .
// .
func TestTurningSpeechOffAsksNoService(t *testing.T) {
	vendor, asked := speakingVendor(t, "refuse", nil)
	a, path := speechSettingsApp(t, vendor.URL)
	a.cfg.Speech.STT = STTConfig{Provider: "Deepgram", Model: "nova-3-general"}
	if _, err := a.applyConfigChange(map[string]interface{}{"speech.stt.provider": "", "speech.tts.provider": ""}); err != nil {
		t.Fatalf("switching speech off was refused: %v", err)
	}
	if n := asked.Load(); n != 0 || a.configSnapshot().Speech.STT.Provider != "" {
		t.Fatalf("switching speech off asked the service %d times", n)
	}
	a.cfg.Speech.STT = STTConfig{Provider: "Deepgram", Model: "nova-3-general"}
	if err := os.WriteFile(path, []byte(`{"providers":[`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := a.applyConfigChange(map[string]interface{}{"speech.stt.provider": ""}); err != nil {
		t.Fatalf("a broken providers.json kept speech from being switched off: %v", err)
	}
}

// .
// .
// .
func TestASpeechCheckIsBoundedByTheOperatorsCeiling(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(release)
	a, _ := speechSettingsApp(t, srv.URL)
	a.cfg.LLM.ProbeTimeoutSeconds = 1
	start := time.Now()
	_, err := a.applyConfigChange(map[string]interface{}{"speech.stt.provider": "Deepgram", "speech.stt.model": "nova-3-general"})
	if took := time.Since(start); err == nil || took > 10*time.Second {
		t.Fatalf("a service that never answered held the save for %s: %v", took, err)
	}
}

// .
// .
// .
func TestAProvidersChangeDuringASpeechCheckRefusesTheSave(t *testing.T) {
	var path string
	vendor, _ := speakingVendor(t, "", func() {
		_ = os.WriteFile(path, []byte(`{"providers":[{"name":"ElevenLabs","url":"https://api.elevenlabs.io","api_key":"other"}]}`), 0o600)
	})
	var a *App
	a, path = speechSettingsApp(t, vendor.URL)
	_, err := a.applyConfigChange(map[string]interface{}{"speech.stt.provider": "Deepgram", "speech.stt.model": "nova-3-general"})
	if err == nil || !strings.Contains(err.Error(), "providers changed") {
		t.Fatalf("a save checked against one registry was committed over another: %v", err)
	}
	if a.configSnapshot().Speech.STT.Provider != "" {
		t.Fatal("the refused save changed what is in force")
	}
}

// .
// .
// .
func TestTheSpeechReadbackSaysWhatIsInForce(t *testing.T) {
	vendor, _ := speakingVendor(t, "", nil)
	a, _ := speechSettingsApp(t, vendor.URL)
	a.cfg.Speech.STT = STTConfig{Provider: "Deepgram", Model: "nova-3-general"}
	a.cfg.Speech.TTS = TTSConfig{Provider: "Nowhere", Voice: "v"}
	st := a.configState().Speech
	if st.STT.Endpoint != vendor.URL+"/v1/listen" || st.STT.APIKeyMasked != "••••1234" || st.STT.Error != "" {
		t.Errorf("input readback %+v", st.STT)
	}
	if !strings.Contains(st.TTS.Error, `"Nowhere"`) || st.TTS.Endpoint != "" {
		t.Errorf("a pointer to nowhere read back as %+v", st.TTS)
	}
	services := map[string]dashboard.SpeechService{}
	for _, s := range st.Services {
		services[s.Name] = s
	}
	if len(services) != len(st.Services) {
		t.Errorf("a service is listed twice: %+v", st.Services)
	}
	if len(st.Services) == 0 || st.Services[0].Name != "OpenAI" {
		t.Errorf("the engines are not listed in the registry's order: %+v", st.Services)
	}
	if el := services["ElevenLabs"]; !el.Added || !el.HasKey || el.Chats || el.Endpoint != vendor.URL {
		t.Errorf("ElevenLabs, in the file with a key, reads %+v", el)
	}
	if hu := services["Hume"]; hu.Added || hu.Speech == nil {
		t.Errorf("Hume, shipped and not in the file, reads %+v", hu)
	}
	if oa := services["OpenAI"]; !oa.Chats || oa.Added {
		t.Errorf("OpenAI, which also chats, reads %+v", oa)
	}
	raw, err := json.Marshal(st)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"language":""`) {
		t.Errorf("a blank language is missing from the wire, so a cleared save cannot read back: %s", raw)
	}
}

// .
// .
func TestAShippedSpeechServiceIsAddedWithTheOperatorsKey(t *testing.T) {
	a := newVoiceApp(t)
	path := filepath.Join(filepath.Dir(a.cfg.SourcePath), "providers.json")
	if err := os.WriteFile(path, []byte(`{"providers":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := a.addShippedSpeechService("Deepgram", " dg-key "); err != nil {
		t.Fatal(err)
	}
	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(written), `"speech"`) {
		t.Errorf("the added entry carries the lent mapping as the operator's own:\n%s", written)
	}
	reg, err := loadProvidersFile(path)
	if err != nil {
		t.Fatal(err)
	}
	shipped := shippedEntry(t, "Deepgram")
	e := entryNamed(reg, "Deepgram")
	if e == nil || e.URL != shipped.URL || e.APIKey != "dg-key" || e.APIKeyEnv != shipped.APIKeyEnv || e.Speech == nil {
		t.Fatalf("added entry %+v", e)
	}
	if err := a.addShippedSpeechService("Deepgram", "again"); err == nil || !strings.Contains(err.Error(), "already") {
		t.Errorf("a second add was not refused: %v", err)
	}
	for _, name := range []string{"Anthropic", "not-a-vendor"} {
		if err := a.addShippedSpeechService(name, "k"); err == nil {
			t.Errorf("%s was added as a speech service", name)
		}
	}
	// .
	// .
	if err := a.addShippedSpeechService("OpenAI", "sk"); err != nil {
		t.Fatal(err)
	}
	if written, err = os.ReadFile(path); err != nil {
		t.Fatal(err)
	}
	for _, fact := range []string{`"speech"`, `"effort_levels"`, `"catalogue_author"`} {
		if strings.Contains(string(written), fact) {
			t.Errorf("an added entry carries %s as the operator's own:\n%s", fact, written)
		}
	}
}

// .
// .
func TestATranscriptionModelIsRequiredOnlyWhereItIsRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "providers.json")
	if err := os.WriteFile(path, []byte(`{"providers":[
		{"name":"dialect","url":"http://127.0.0.1:9"},
		{"name":"no-model","url":"http://127.0.0.1:9","speech":{"stt":{"fields":{}}}}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	reg, err := loadProvidersFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := transcriberFor(STTConfig{Provider: "dialect"}, reg); err == nil || !strings.Contains(err.Error(), "speech.stt.model") {
		t.Errorf("OpenAI's dialect reads a model, and none was required: %v", err)
	}
	if _, _, err := transcriberFor(STTConfig{Provider: "no-model"}, reg); err != nil {
		t.Errorf("a service that reads no model was refused for lacking one: %v", err)
	}
}

// .
// .
func TestTheLivePageCanAddASpeechService(t *testing.T) {
	a := newVoiceApp(t)
	path := filepath.Join(filepath.Dir(a.cfg.SourcePath), "providers.json")
	if err := os.WriteFile(path, []byte(`{"providers":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	h := a.buildLiveHandler()
	if h.SetSpeechService == nil {
		t.Fatal("the live handler cannot add a speech service")
	}
	if err := h.SetSpeechService("Hume", "hu", ""); err != nil {
		t.Fatal(err)
	}
	reg, err := a.loadProviders()
	if err != nil || entryNamed(reg, "Hume") == nil {
		t.Fatalf("the live handler's add did not reach providers.json: %v", err)
	}
}

// .
// .
// .
func TestAKeyTypedForASpeechServiceIsStoredOnItsEntry(t *testing.T) {
	vendor, _ := speakingVendor(t, "", nil)
	a, path := speechSettingsApp(t, vendor.URL)
	stored := func(name string) string {
		t.Helper()
		reg, err := loadProvidersFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if e := entryNamed(reg, name); e != nil {
			return e.APIKey
		}
		return "(absent)"
	}
	if err := a.setSpeechService("ElevenLabs", " typed-key ", ""); err != nil {
		t.Fatal(err)
	}
	if got := stored("ElevenLabs"); got != "typed-key" {
		t.Fatalf("the typed key was not stored on the entry: %q", got)
	}
	if err := a.setSpeechService("ElevenLabs", "", ""); err != nil {
		t.Fatal(err)
	}
	if got := stored("ElevenLabs"); got != "typed-key" {
		t.Fatalf("a blank key replaced the stored one: %q", got)
	}
	if err := a.setSpeechService("Deepgram", "dg", ""); err != nil {
		t.Fatal(err)
	}
	if got := stored("Deepgram"); got != "dg" {
		t.Fatalf("a shipped service the file lacked was not added with its key: %q", got)
	}
	if err := a.setSpeechService("not-a-vendor", "k", ""); err == nil {
		t.Fatal("a service this release does not ship was added")
	}
}

// .
// .
// .
func TestASpeechServiceWithNoKeySaysSo(t *testing.T) {
	t.Setenv("DEEPGRAM_API_KEY", "")
	vendor, _ := speakingVendor(t, "refuse", nil)
	a := newVoiceApp(t)
	path := filepath.Join(filepath.Dir(a.cfg.SourcePath), "providers.json")
	if err := os.WriteFile(path, []byte(`{"providers":[{"name":"Deepgram","url":"`+vendor.URL+`"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := a.applyConfigChange(map[string]interface{}{"speech.stt.provider": "Deepgram", "speech.stt.model": "nova-3-general"})
	if err == nil || !strings.Contains(err.Error(), "no API key is stored for Deepgram") || !strings.Contains(err.Error(), "invalid api key") {
		t.Fatalf("a keyless refusal did not say the key is missing: %v", err)
	}

	keyed, _ := speechSettingsApp(t, vendor.URL)
	_, err = keyed.applyConfigChange(map[string]interface{}{"speech.stt.provider": "Deepgram", "speech.stt.model": "nova-3-general"})
	if err == nil || strings.Contains(err.Error(), "no API key is stored") || !strings.Contains(err.Error(), "invalid api key") {
		t.Fatalf("a refused key was reported as a missing one: %v", err)
	}
}

// .
// .
// .
// .
func TestAnOpenAICompatibleServerBecomesItsOwnSpeechEntry(t *testing.T) {
	vendor, _ := speakingVendor(t, "", nil)
	a, path := speechSettingsApp(t, vendor.URL)
	const name = "OpenAI-compatible · localhost:8000"
	if err := a.setSpeechService(name, "", "http://localhost:8000/v1"); err != nil {
		t.Fatal(err)
	}
	reg, err := loadProvidersFile(path)
	if err != nil {
		t.Fatal(err)
	}
	e := entryNamed(reg, name)
	if e == nil || e.URL != "http://localhost:8000/v1" || e.APIKey != "" || !speechOnly(*e) || e.Speech.STT == nil || e.Speech.TTS == nil {
		t.Fatalf("own server entry %+v", e)
	}
	if err := a.setSpeechService(name, "local-key", "http://localhost:8001/v1"); err != nil {
		t.Fatal(err)
	}
	if reg, err = loadProvidersFile(path); err != nil {
		t.Fatal(err)
	}
	if e = entryNamed(reg, name); e.URL != "http://localhost:8001/v1" || e.APIKey != "local-key" {
		t.Fatalf("the own server was not moved and keyed in place: %+v", e)
	}
	if err := a.setSpeechService(name, "", "http://localhost:8001/v1"); err != nil {
		t.Fatal(err)
	}
	if reg, err = loadProvidersFile(path); err != nil {
		t.Fatal(err)
	}
	if e = entryNamed(reg, name); e.APIKey != "local-key" {
		t.Fatalf("saving the server again with no key typed dropped its key: %+v", e)
	}
	services := speechServices(reg)
	if last := services[len(services)-1]; last.Name != name || !last.Custom || !last.Added || !last.HasKey || last.Chats {
		t.Fatalf("the own server is not listed last as the operator's: %+v", last)
	} else if last.Speech == nil || last.Speech.STT == nil || !last.Speech.STT.ModelRequired || last.Speech.TTS == nil || !last.Speech.TTS.VoiceRequired {
		t.Fatalf("the own server does not speak OpenAI's dialect, a model both ways and a voice: %+v", last.Speech)
	}

	// .
	// .
	if err := os.WriteFile(path, []byte(`{"providers":[{"name":"local-chat","url":"http://127.0.0.1:1/v1","default_model":"m","speech":{"stt":{}}}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if reg, err = loadProvidersFile(path); err != nil {
		t.Fatal(err)
	}
	var chat *dashboard.SpeechService
	for _, s := range speechServices(reg) {
		if s.Name == "local-chat" {
			chat = &s
		}
	}
	if chat == nil || !chat.Chats || chat.Custom || !chat.Added {
		t.Fatalf("a chat provider that declares speech reads %+v", chat)
	}
	for _, tc := range []struct{ why, name, url string }{
		{"a shipped provider", "OpenAI", "http://localhost:9/v1"},
		{"a shipped speech service", "ElevenLabs", "http://localhost:9/v1"},
		{"a chat provider in the file", "local-chat", "http://localhost:9/v1"},
		{"an address that is not a URL", name, "ftp://localhost:9"},
		{"no name", "", "http://localhost:9/v1"},
	} {
		if err := a.setSpeechService(tc.name, "", tc.url); err == nil {
			t.Errorf("%s was accepted as an OpenAI-compatible speech server", tc.why)
		}
	}
}
