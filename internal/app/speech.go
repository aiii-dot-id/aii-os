package app

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/speech"
)

// speech.go — where spoken words enter, and where they come from.
//
// THE MICROPHONE IS AT THE BROWSER. In every deployment that exists the
// operator is at a browser and the identity is somewhere else, so the
// browser captures, and one complete utterance crosses the dashboard
// socket that already spans that link. The host transcribes it against a
// configured endpoint — the same way it thinks against a configured
// endpoint — and hands the words to the attribution path in voice.go.
//
// There is no plugin in that sentence, and no transport. Both existed
// and both were discarded (docs/VOICE_FIRST_PRINCIPLES.md) once the
// probe showed a contained plugin cannot reach a socket and the First
// Principles pass showed the speech model never needed to be contained
// in the first place — the identity's own mind is a remote endpoint it
// does not sandbox.
//
// OFF UNTIL POINTED SOMEWHERE. speech.stt.provider empty means the
// identity has no microphone: the browser is told, and it does not offer
// a control that cannot work. An identity that boots with voice
// unconfigured is not degraded, it is simply not listening.

// resolveSpeech builds the transcription client, or reports that voice
// is not configured.
//
// IT RESOLVES A POINTER, EXACTLY LIKE THE SUBSTRATE. providers.json is
// the provider data; config.json names an entry. A pointer that does not
// dereference is an error the operator can act on — naming a provider
// that is not there is a typo, and a typo that silently disables the
// microphone is worse than one that says so.
func (a *App) resolveSpeech() (*speech.Client, error) {
	cfg := a.configSnapshot()
	sc := cfg.Speech.STT
	if strings.TrimSpace(sc.Provider) == "" {
		return nil, nil // not configured; not an error
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

// VoiceConfigured reports whether the browser should offer a microphone.
// Asked live rather than latched at boot: an operator who configures a
// speech endpoint and reloads should get the control without restarting
// the identity.
func (a *App) VoiceConfigured() bool {
	// SAFE OFFERS NO MICROPHONE — AND THIS IS THE INDICATION, NOT THE
	// ENFORCEMENT. A page already open when SAFE begins keeps whatever
	// control it last rendered, so this line cannot be what makes SAFE
	// true; HearUtterance is, because every utterance passes through it.
	// What this line does is stop the identity inviting the operator to
	// speak into something that is going to refuse them.
	if _, inSafe := a.SafeMode(); inSafe {
		return false
	}
	c, err := a.resolveSpeech()
	return err == nil && c != nil
}

// HearUtterance transcribes one complete utterance and enters it as
// something the identity heard.
//
// ONE UTTERANCE, WHOLE. The browser decides where an utterance ends,
// because the browser is holding the microphone and knows when the
// operator stopped talking. Nothing here assembles chunks or tracks a
// session — the simplest thing that can carry speech, and the thing a
// streaming engine could later hide behind without any caller noticing.
//
// IT RUNS ON THE CALLER'S GOROUTINE AND THAT CALLER MUST NOT BE A READER.
// Transcription is a network round trip and admission can end in a whole
// LLM turn; doing either on a socket's read loop stops the socket being
// read, and the next thing on it might be the operator interrupting.
// The dashboard hands this to a worker for exactly that reason.
func (a *App) HearUtterance(ctx context.Context, pcm []byte, sampleRate, channels int) error {
	// SAFE REFUSES BEFORE THE AUDIO MOVES, NOT BEFORE IT IS RECORDED.
	//
	// The broker has refused voice.observe under SAFE since the stream
	// carrier went away, and that refusal was carefully preserved. Then
	// this path was built beside it and never asked — and this is the
	// path a browser actually uses, so SAFE held the microphone shut on
	// the door nobody walks through.
	//
	// The check belongs HERE, above the transcription, and not at
	// admission. Gating admission would keep the words out of the record
	// while the audio had already crossed the network to a third party,
	// and for SAFE the leaving IS the violation: an identity that cannot
	// trust its own record must not be shipping a room to an endpoint on
	// the strength of that record's own configuration.
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
		// Silence, or a model that heard none of it. Not an error: an
		// operator who taps the microphone and says nothing has not
		// done anything wrong, and a toast saying so is noise.
		log.Printf("VOICE: %d bytes of audio transcribed to nothing", len(pcm))
		return nil
	}
	return a.observeVoice(ctx, heardUtterance{
		// SOURCE IS HOST-SET, and this is the whole reason it is not
		// called PluginID any more. Two things can now propose an
		// utterance — a plugin through the broker, and this endpoint —
		// and the value of naming the proposer is being able to
		// disbelieve one later. Neither of them writes this string.
		Source: "speech endpoint " + client.Model(),
		Text:   res.Text,
		// NO SPEAKER, AND NO GUESS AT ONE. A transcription engine
		// reports words, not people. Inventing "operator" here is
		// precisely the acoustics-to-authority path the whole design
		// refuses — the utterance enters as a participant turn with an
		// unidentified speaker, which is the truth.
	})
}
