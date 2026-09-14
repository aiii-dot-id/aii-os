package app

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

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
func (a *App) resolveSpeech() (*speech.Client, error) {
	cfg := a.configSnapshot()
	sc := cfg.Speech.STT
	if strings.TrimSpace(sc.Provider) == "" {
		return nil, nil
	}
	reg, err := a.loadProviders()
	if err != nil {
		return nil, err
	}
	var entry *providerEntry
	for i := range reg.Providers {
		if reg.Providers[i].Name == sc.Provider {
			entry = &reg.Providers[i]
			break
		}
	}
	if entry == nil {
		return nil, fmt.Errorf("speech.stt.provider %q is not in providers.json (%d providers)", sc.Provider, len(reg.Providers))
	}
	model := sc.Model
	if model == "" {
		return nil, fmt.Errorf("speech.stt.model is empty — a transcription endpoint needs a model name")
	}
	timeout := time.Duration(sc.TimeoutSeconds) * time.Second
	return speech.New(speech.Config{
		Endpoint: entry.URL,
		Model:    model,
		APIKey:   providerAPIKey(*entry, "", sc.APIKeyEnv),
		Language: sc.Language,
		Timeout:  timeout,
	}), nil
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
	client, err := a.resolveSpeech()
	if err != nil {
		return err
	}
	if client == nil {
		return fmt.Errorf("no speech endpoint is configured — set speech.stt.provider and speech.stt.model")
	}
	res, err := client.Transcribe(ctx, pcm, sampleRate, channels)
	if err != nil {
		return err
	}
	if strings.TrimSpace(res.Text) == "" {
		// .
		// .
		// .
		log.Printf("VOICE: %d bytes of audio transcribed to nothing", len(pcm))
		return nil
	}
	return a.observeVoice(ctx, heardUtterance{
		// .
		// .
		// .
		// .
		// .
		Source: "speech endpoint " + client.Model(),
		Text:   res.Text,
		// .
		// .
		// .
		// .
		// .
		Answer: answer,
	})
}
