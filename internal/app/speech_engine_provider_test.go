package app

import (
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
	"github.com/aiii-dot-id/aii-os/internal/supervisor"
)

// .
// .
// .
func engineOnThisMachine(t *testing.T, a *App, id, title string) {
	t.Helper()
	c := supervisor.NewSessionClientFrames(make(chan []byte, 4), io.Discard, nil, 8)
	v := pluginhost.NewVoiceSession(c)
	a.pluginMu.Lock()
	a.plugins = append(a.plugins, &pluginhost.ActivePlugin{ID: id, Title: title, Voice: v})
	a.pluginMu.Unlock()
	if !a.VoiceEngine() {
		t.Fatal("fixture: the engine is not active")
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
// .
// .
// .
// .
func TestTheInstalledEngineCanBeChosenInSettingsSpeech(t *testing.T) {
	dir := t.TempDir()
	a := New(&Config{SourcePath: filepath.Join(dir, "config.json")})
	engineOnThisMachine(t, a, "id.aiii.voice", "AII Voice")

	cfg := a.configSnapshot()
	cfg.Speech.STT.Provider = "id.aiii.voice"
	cfg.Speech.TTS.Provider = "id.aiii.voice"
	// .
	// .
	if err := a.checkSpeech(&cfg, &providerRegistry{}, true, true); err != nil {
		t.Fatalf("the installed engine must be choosable for both directions: %v", err)
	}

	// .
	// .
	other := a.configSnapshot()
	other.Speech.TTS.Provider = "Deepgram"
	err := a.checkSpeech(&other, &providerRegistry{}, false, true)
	if err == nil || !strings.Contains(err.Error(), "not in providers.json") {
		t.Fatalf("a service that is not there is still refused: %v", err)
	}
	// .
	bare := New(&Config{SourcePath: filepath.Join(dir, "config.json")})
	cfg2 := bare.configSnapshot()
	cfg2.Speech.TTS.Provider = "id.aiii.voice"
	if err := bare.checkSpeech(&cfg2, &providerRegistry{}, false, true); err == nil {
		t.Fatal("with no engine installed its id resolves to nothing and must be refused")
	}
}

// .
// .
// .
// .
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

	// .
	// .
	a.cfgMu.Lock()
	a.cfg.Speech.TTS.Provider = "ElevenLabs"
	a.cfgMu.Unlock()
	if v := a.replyVoice(); v != "ElevenLabs" {
		t.Fatalf("a service the operator named must still speak replies: %q", v)
	}
}
