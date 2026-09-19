package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/speech"
)

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
// .
// .
// .
// .
// .
func (a *App) resolveSpeech() (*speech.Client, error) {
	sc := a.configSnapshot().Speech.STT
	if strings.TrimSpace(sc.Provider) == "" {
		return nil, nil
	}
	if a.engineServes(sc.Provider) {
		return nil, nil
	}
	reg, err := a.loadProviders()
	if err != nil {
		return nil, err
	}
	c, _, err := transcriberFor(sc, reg)
	return c, err
}

// .
// .
// .
// .
func transcriberFor(sc STTConfig, reg *providerRegistry) (*speech.Client, *providerEntry, error) {
	entry := entryNamed(reg, sc.Provider)
	if entry == nil {
		return nil, nil, fmt.Errorf("speech.stt.provider %q is not in providers.json (%d providers)", sc.Provider, providerCount(reg))
	}
	svc := entry.Speech.service(speech.STT)
	if svc.Uses(speech.STT, "model") && sc.Model == "" {
		return nil, nil, fmt.Errorf("speech.stt.model is empty — %s needs a transcription model", entry.Name)
	}
	return speech.New(speech.Config{
		Endpoint: entry.URL,
		Model:    sc.Model,
		APIKey:   providerAPIKey(*entry, "", sc.APIKeyEnv),
		Language: sc.Language,
		Timeout:  time.Duration(sc.TimeoutSeconds) * time.Second,
		Service:  svc,
	}), entry, nil
}

// .
// .
func (a *App) resolveTTS() (*speech.Synthesizer, error) {
	tc := a.configSnapshot().Speech.TTS
	if strings.TrimSpace(tc.Provider) == "" {
		return nil, nil
	}
	if a.engineServes(tc.Provider) {
		return nil, nil
	}
	reg, err := a.loadProviders()
	if err != nil {
		return nil, err
	}
	s, _, err := synthesizerFor(tc, reg)
	return s, err
}

// .
// .
// .
// .
// .
func synthesizerFor(tc TTSConfig, reg *providerRegistry) (*speech.Synthesizer, *providerEntry, error) {
	entry := entryNamed(reg, tc.Provider)
	if entry == nil {
		return nil, nil, fmt.Errorf("speech.tts.provider %q is not in providers.json (%d providers)", tc.Provider, providerCount(reg))
	}
	s, err := synthesizerForEntry(tc, entry, "")
	return s, entry, err
}

// .
// .
// .
func speechEntryNamed(reg *providerRegistry, name string) *providerEntry {
	if e := entryNamed(reg, name); e != nil {
		return e
	}
	for _, s := range embeddedRegistry().Providers {
		if s.Name == name && s.Speech != nil {
			shipped := s
			return &shipped
		}
	}
	return nil
}

// .
// .
func synthesizerForEntry(tc TTSConfig, entry *providerEntry, typedKey string) (*speech.Synthesizer, error) {
	svc := entry.Speech.service(speech.TTS)
	if svc.Uses(speech.TTS, "model") && tc.Model == "" {
		return nil, fmt.Errorf("speech.tts.model is empty — %s needs a model name", entry.Name)
	}
	if svc.Uses(speech.TTS, "voice") && tc.Voice == "" {
		return nil, fmt.Errorf("speech.tts.voice is empty — %s needs a voice", entry.Name)
	}
	maxChars := 0
	if o := entry.Speech.offer(speech.TTS); o != nil {
		maxChars = o.MaxChars
	}
	return speech.NewSynthesizer(speech.SynthConfig{
		Endpoint: entry.URL,
		Model:    tc.Model,
		Voice:    tc.Voice,
		APIKey:   providerAPIKey(*entry, typedKey, tc.APIKeyEnv),
		MaxChars: maxChars,
		Timeout:  time.Duration(tc.TimeoutSeconds) * time.Second,
		Service:  svc,
	}), nil
}

// .
// .
// .
func providerCount(reg *providerRegistry) int {
	if reg == nil {
		return 0
	}
	return len(reg.Providers)
}

func entryNamed(reg *providerRegistry, name string) *providerEntry {
	if reg == nil {
		return nil
	}
	for i := range reg.Providers {
		if reg.Providers[i].Name == name {
			return &reg.Providers[i]
		}
	}
	return nil
}

// .
// .
// .
// .
var checkSilence = make([]byte, 16000)

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
// .
// .
func (a *App) engineServes(provider string) bool {
	if strings.TrimSpace(provider) == "" {
		return false
	}
	_, serves, _ := speechEngineServes(provider, a.speechEngines())
	return serves
}

// .
// .
// .
// .
// .
// .
// .
// .
func (a *App) checkSpeech(cfg *Config, reg *providerRegistry, input, output bool) error {
	ctx := a.bgCtx
	if ctx == nil {
		ctx = context.Background()
	}
	if input && strings.TrimSpace(cfg.Speech.STT.Provider) != "" && !a.engineServes(cfg.Speech.STT.Provider) {
		c, entry, err := transcriberFor(cfg.Speech.STT, reg)
		if err != nil {
			return fmt.Errorf("voice input refused: %w", err)
		}
		cctx, cancel := context.WithTimeout(ctx, checkTimeout(cfg.Speech.STT.TimeoutSeconds, cfg.LLM.ProbeTimeoutSeconds))
		_, err = c.Transcribe(cctx, checkSilence, 16000, 1)
		cancel()
		if err != nil {
			return fmt.Errorf("voice input refused: %s", checkRefusal(cfg.Speech.STT.Provider, "did not transcribe a moment of silence",
				providerAPIKey(*entry, "", cfg.Speech.STT.APIKeyEnv) == "", err))
		}
		// .
		a.meterSpeech(cfg.Speech.STT.Provider, "stt", 0, audioDuration(len(checkSilence), 16000, 1))
		a.speechTrouble.clear()
		pushVoiceStatus(a)
	}
	if output && strings.TrimSpace(cfg.Speech.TTS.Provider) != "" && !a.engineServes(cfg.Speech.TTS.Provider) {
		s, entry, err := synthesizerFor(cfg.Speech.TTS, reg)
		if err != nil {
			return fmt.Errorf("voice replies refused: %w", err)
		}
		cctx, cancel := context.WithTimeout(ctx, checkTimeout(cfg.Speech.TTS.TimeoutSeconds, cfg.LLM.ProbeTimeoutSeconds))
		// .
		err = s.Synthesize(cctx, "Ready.", func(speech.Audio) error { return nil })
		cancel()
		if err != nil {
			return fmt.Errorf("voice replies refused: %s", checkRefusal(cfg.Speech.TTS.Provider, "did not speak one word",
				providerAPIKey(*entry, "", cfg.Speech.TTS.APIKeyEnv) == "", err))
		}
		a.meterSpeech(cfg.Speech.TTS.Provider, "tts", len("Ready."), 0)
	}
	return nil
}

// .
// .
func audioDuration(bytes, rate, channels int) time.Duration {
	if rate <= 0 || channels <= 0 {
		return 0
	}
	return time.Duration(float64(bytes) / float64(2*rate*channels) * float64(time.Second))
}

// .
// .
// .
func checkRefusal(service, failed string, keyless bool, err error) string {
	var r *speech.Refusal
	if keyless && errors.As(err, &r) && (r.Status == http.StatusUnauthorized || r.Status == http.StatusForbidden) {
		return fmt.Sprintf("no API key is stored for %s — enter one in Settings → Speech (%s said: %s)", service, service, r.Said)
	}
	return fmt.Sprintf("%s %s: %v", service, failed, err)
}

// .
// .
// .
// .
func keyed(e providerEntry) bool {
	return e.APIKey != "" || (e.APIKeyEnv != "" && os.Getenv(e.APIKeyEnv) != "")
}

// .
// .
// .
func speechState(c Config, reg *providerRegistry, regErr error, engines []dashboard.SpeechService) dashboard.SpeechConfigState {
	sc, tc := c.Speech.STT, c.Speech.TTS
	st := dashboard.SpeechConfigState{
		STT: dashboard.SpeechInputState{Provider: sc.Provider, Model: sc.Model, Language: sc.Language, MonthlyMinutes: sc.MonthlyMinutes},
		TTS: dashboard.SpeechOutputState{Provider: tc.Provider, Model: tc.Model, Voice: tc.Voice, MonthlyCharacters: tc.MonthlyCharacters},
	}
	// .
	// .
	// .
	// .
	st.Services = append([]dashboard.SpeechService(nil), engines...)
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
	st.Services = append(st.Services, speechServices(reg)...)

	// .
	// .
	// .
	// .
	sttPlugin, sttServes, sttDefault := speechEngineServes(sc.Provider, engines)
	if sttServes {
		st.STT.Provider, st.STT.Plugin, st.STT.Default = sttPlugin, true, sttDefault
	}
	ttsPlugin, ttsServes, ttsDefault := speechEngineServes(tc.Provider, engines)
	if ttsServes {
		st.TTS.Provider, st.TTS.Plugin, st.TTS.Default = ttsPlugin, true, ttsDefault
	}
	if sttServes && ttsServes {
		return st
	}

	inputSet := !sttServes && strings.TrimSpace(sc.Provider) != ""
	outputSet := !ttsServes && strings.TrimSpace(tc.Provider) != ""
	if regErr != nil {
		if inputSet {
			st.STT.Error = regErr.Error()
		}
		if outputSet {
			st.TTS.Error = regErr.Error()
		}
		return st
	}
	if inputSet {
		if cl, entry, err := transcriberFor(sc, reg); err != nil {
			st.STT.Error = err.Error()
		} else {
			st.STT.Endpoint = cl.Endpoint()
			st.STT.APIKeyMasked = maskKey(providerAPIKey(*entry, "", sc.APIKeyEnv))
		}
	}
	if outputSet {
		if s, entry, err := synthesizerFor(tc, reg); err != nil {
			st.TTS.Error = err.Error()
		} else {
			st.TTS.Endpoint = s.Endpoint()
			st.TTS.APIKeyMasked = maskKey(providerAPIKey(*entry, "", tc.APIKeyEnv))
		}
	}
	return st
}

// .
// .
// .
// .
// .
func speechServices(reg *providerRegistry) []dashboard.SpeechService {
	if reg == nil {
		return nil
	}
	var out []dashboard.SpeechService
	shipped := map[string]bool{}
	for _, e := range embeddedRegistry().Providers {
		if e.Speech == nil {
			continue
		}
		shipped[e.Name] = true
		svc := dashboard.SpeechService{Name: e.Name, Endpoint: e.URL, APIKeyEnv: e.APIKeyEnv, Speech: speechInfo(e), Chats: chatProvider(e)}
		if own := entryNamed(reg, e.Name); own != nil {
			svc.Added, svc.HasKey, svc.Endpoint = true, keyed(*own), own.URL
			if own.APIKeyEnv != "" {
				svc.APIKeyEnv = own.APIKeyEnv
			}
			if own.Speech != nil {
				svc.Speech = speechInfo(*own)
			}
		}
		out = append(out, svc)
	}
	for _, e := range reg.Providers {
		if e.Speech == nil || shipped[e.Name] {
			continue
		}
		out = append(out, dashboard.SpeechService{Name: e.Name, Endpoint: e.URL, APIKeyEnv: e.APIKeyEnv, Speech: speechInfo(e),
			Added: true, HasKey: keyed(e), Chats: chatProvider(e), Custom: !chatProvider(e)})
	}
	return out
}

// .
// .
// .
// .
// .
func (a *App) VoiceStatus() (state, reason, source string) {
	if why, inSafe := a.SafeMode(); inSafe {
		return "safe", why, ""
	}
	sc := a.configSnapshot().Speech.STT
	chosen := strings.TrimSpace(sc.Provider)
	// .
	// .
	// .
	// .
	// .
	if id := activeVoicePlugin(a); id != "" && (chosen == "" || chosen == id) {
		return "plugin", "", id
	}
	if chosen == "" {
		return "setup", "", ""
	}
	c, err := a.resolveSpeech()
	if err != nil {
		return "unreachable", err.Error(), sc.Provider
	}
	if c == nil {
		return "setup", "", ""
	}
	source = sc.Provider
	if c.Model() != "" {
		source += " · " + c.Model()
	}
	if why := a.speechTrouble.current(sc); why != "" {
		return "unreachable", why, source
	}
	return "cloud", "", source
}

// .
// .
// .
var activeVoicePlugin = func(a *App) string {
	if p := a.voicePlugin(); p != nil {
		return p.ID
	}
	return ""
}

// .
// .
// .
var pushVoiceStatus = func(a *App) {
	if a.dashboard != nil {
		a.dashboard.BroadcastStatus()
	}
}

// .
// .
// .
// .
type speechTrouble struct {
	mu     sync.Mutex
	under  STTConfig
	reason string
}

func (t *speechTrouble) note(under STTConfig, err error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.under, t.reason = under, err.Error()
}

func (t *speechTrouble) clear() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.reason = ""
}

func (t *speechTrouble) current(under STTConfig) string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.under != under {
		return ""
	}
	return t.reason
}

// .
// .
// .
// .
func (a *App) VoiceConfigured() bool {
	// .
	// .
	// .
	// .
	// .
	// .
	if _, inSafe := a.SafeMode(); inSafe {
		return false
	}
	c, err := a.resolveSpeech()
	return err == nil && c != nil
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
// .
// .
func (a *App) HearUtterance(ctx context.Context, pcm []byte, sampleRate, channels int, answer bool) error {
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
	// .
	// .
	if reason, inSafe := a.SafeMode(); inSafe {
		return fmt.Errorf("this identity is in SAFE; nothing heard is recorded while it holds (%s)", reason)
	}
	sc := a.configSnapshot().Speech.STT
	client, err := a.resolveSpeech()
	if err != nil {
		return err
	}
	if client == nil {
		return fmt.Errorf("no speech endpoint is configured — set speech.stt.provider and speech.stt.model")
	}
	// .
	// .
	heard := audioDuration(len(pcm), sampleRate, channels)
	if err := a.reserveHearing(heard); err != nil {
		a.speechTrouble.note(sc, err)
		pushVoiceStatus(a)
		return err
	}
	defer a.settleHearing(heard)
	res, err := client.Transcribe(ctx, pcm, sampleRate, channels)
	if err != nil {
		// .
		// .
		a.speechTrouble.note(sc, err)
		pushVoiceStatus(a)
		return err
	}
	a.meterSpeech(sc.Provider, "stt", 0, heard)
	if a.speechTrouble.current(sc) != "" {
		a.speechTrouble.clear()
		pushVoiceStatus(a)
	}
	if strings.TrimSpace(res.Text) == "" {
		// .
		// .
		// .
		log.Printf("VOICE: %d bytes of audio transcribed to nothing", len(pcm))
		return nil
	}
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	if answer {
		// .
		// .
		a.fanVoiceEvent(dashboard.VoiceEvent{Type: "transcript_final", Final: true, Operator: true, Text: strings.TrimSpace(res.Text)})
	}
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	listen, _, _ := a.voiceMode()
	return a.observeVoice(ctx, heardUtterance{
		// .
		// .
		Source:   "speech endpoint " + client.Model(),
		Text:     res.Text,
		Answer:   answer && listen != listenMeeting,
		Operator: true,
	})
}
