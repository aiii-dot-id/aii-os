package app

import (
	"encoding/json"
	"fmt"
	"strings"

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
type speechBlock struct {
	STT *speechOffer `json:"stt,omitempty"`
	TTS *speechOffer `json:"tts,omitempty"`
}

// .
// .
// .
type speechOffer struct {
	speech.Service
	// .
	MaxChars int `json:"max_chars,omitempty"`
	// .
	// .
	// .
	// .
	// .
	ListModels *speech.List `json:"list_models,omitempty"`
	ListVoices *speech.List `json:"list_voices,omitempty"`
}

// .
// .
func (b *speechBlock) service(dir speech.Direction) *speech.Service {
	o := b.offer(dir)
	if o == nil {
		return nil
	}
	s := o.Service
	return &s
}

func (b *speechBlock) offer(dir speech.Direction) *speechOffer {
	if b == nil {
		return nil
	}
	if dir == speech.TTS {
		return b.TTS
	}
	return b.STT
}

// .
// .
// .
func speechOnly(e providerEntry) bool {
	return e.Speech != nil && e.DefaultModel == "" && len(e.Models) == 0
}

// .
// .
// .
// .
// .
// .
func chatProvider(e providerEntry) bool {
	if e.Chat != nil {
		return *e.Chat
	}
	if shipped := entryNamed(embeddedRegistry(), e.Name); shipped != nil {
		return shipped.Chat == nil || *shipped.Chat
	}
	return !speechOnly(e)
}

// .
// .
// .
// .
// .
// .
// .
func validateRole(e *providerEntry) error {
	if e.Chat != nil && !*e.Chat && e.Speech == nil && !shippedSpeechVendor(e.Name) {
		return fmt.Errorf("provider %q says chat: false, declares no speech, and is not a speech vendor this release ships — add a speech block or remove the entry", e.Name)
	}
	if e.Default && !chatProvider(*e) {
		return fmt.Errorf("provider %q is speech-only but flagged default — the default provider is what the identity thinks with", e.Name)
	}
	return nil
}

// .
// .
func validateSpeech(e *providerEntry) error {
	if e.Speech == nil {
		return nil
	}
	for _, d := range []speech.Direction{speech.STT, speech.TTS} {
		if o := e.Speech.offer(d); o != nil {
			if err := o.Validate(d); err != nil {
				return fmt.Errorf("provider %q speech.%s: %w", e.Name, d, err)
			}
			for _, l := range []struct {
				field string
				list  *speech.List
			}{{"list_models", o.ListModels}, {"list_voices", o.ListVoices}} {
				if l.list == nil {
					continue
				}
				if err := l.list.Validate(); err != nil {
					return fmt.Errorf("provider %q speech.%s.%s: %w", e.Name, d, l.field, err)
				}
			}
		}
	}
	return nil
}

// .
// .
func fillEmbeddedSpeech(reg *providerRegistry) {
	shipped := make(map[string]*speechBlock)
	for _, e := range embeddedRegistry().Providers {
		if e.Speech != nil {
			shipped[e.Name] = e.Speech
		}
	}
	for i := range reg.Providers {
		e := &reg.Providers[i]
		if e.Speech != nil {
			continue
		}
		b, ok := shipped[e.Name]
		if !ok {
			continue
		}
		e.Speech = cloneSpeech(b)
		if reg.filledSpeech == nil {
			reg.filledSpeech = map[string]bool{}
		}
		reg.filledSpeech[e.Name] = true
	}
}

// .
// .
func cloneSpeech(b *speechBlock) *speechBlock {
	if b == nil {
		return nil
	}
	raw, err := json.Marshal(b)
	if err != nil {
		return nil
	}
	var out speechBlock
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil
	}
	return &out
}

// .
// .
func speechInfo(e providerEntry) *dashboard.ProviderSpeech {
	if e.Speech == nil {
		return nil
	}
	info := &dashboard.ProviderSpeech{}
	for _, d := range []speech.Direction{speech.STT, speech.TTS} {
		o := e.Speech.offer(d)
		if o == nil {
			continue
		}
		so := &dashboard.SpeechOffer{
			MaxChars:      o.MaxChars,
			ModelRequired: o.Uses(d, "model"),
			VoiceRequired: d == speech.TTS && o.Uses(d, "voice"),
			ListsModels:   o.ListModels != nil,
			ListsVoices:   o.ListVoices != nil,
		}
		if o.ListVoices != nil {
			so.SearchesVoices = o.ListVoices.Search != ""
		}
		if d == speech.TTS {
			info.TTS = so
		} else {
			info.STT = so
		}
	}
	return info
}

// .
// .
// .
// .
// .
// .
// .
// .
func scaffoldProviders() []byte {
	var reg providerRegistry
	if err := json.Unmarshal(embeddedProviders, &reg); err != nil {
		return embeddedProviders
	}
	reg.OAuth = nil
	for i := range reg.Providers {
		reg.Providers[i].Speech = nil
	}
	data, err := json.MarshalIndent(&reg, "", "  ")
	if err != nil {
		return embeddedProviders
	}
	return data
}

// .
// .
// .
// .
// .
func (a *App) setSpeechService(name, apiKey, baseURL string) error {
	apiKey, baseURL = strings.TrimSpace(apiKey), strings.TrimSpace(baseURL)
	if baseURL != "" {
		return a.setOwnSpeechServer(name, apiKey, baseURL)
	}
	reg, err := a.loadProviders()
	if err != nil {
		return err
	}
	if entryNamed(reg, name) == nil {
		return a.addShippedSpeechService(name, apiKey)
	}
	if apiKey == "" {
		return nil
	}
	return a.changeProviders(name, func(r *providerRegistry) error {
		e := entryNamed(r, name)
		if e == nil {
			return fmt.Errorf("no provider named %q", name)
		}
		e.APIKey = apiKey
		return nil
	})
}

// .
// .
// .
// .
func (a *App) setOwnSpeechServer(name, apiKey, baseURL string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("an OpenAI-compatible speech server needs a name")
	}
	if !validProviderURL(baseURL) {
		return fmt.Errorf("%q is not a base URL (http(s)://host[:port][/path])", baseURL)
	}
	for _, e := range embeddedRegistry().Providers {
		if e.Name == name {
			return fmt.Errorf("%s is a shipped provider; its address is not set from speech settings", name)
		}
	}
	reg, err := a.loadProviders()
	if err != nil {
		return err
	}
	if own := entryNamed(reg, name); own != nil && chatProvider(*own) {
		return fmt.Errorf("%s is a chat provider in providers.json; give the speech server another name", name)
	}
	return a.changeProviders(name, func(r *providerRegistry) error {
		chats := false
		if e := entryNamed(r, name); e != nil {
			e.URL = baseURL
			e.Chat = &chats
			if apiKey != "" {
				e.APIKey = apiKey
			}
			return nil
		}
		// .
		// .
		// .
		// .
		r.Providers = append(r.Providers, providerEntry{Name: name, APIType: "openai", URL: baseURL, APIKey: apiKey, Chat: &chats,
			Speech: &speechBlock{
				STT: &speechOffer{ListModels: openAICompatibleModels()},
				TTS: &speechOffer{ListModels: openAICompatibleModels(), ListVoices: openAICompatibleVoices()},
			}})
		return nil
	})
}

// .
// .
func openAICompatibleModels() *speech.List {
	return &speech.List{Path: "/models", Items: "$.data", ID: "$.id"}
}

func openAICompatibleVoices() *speech.List {
	return &speech.List{Path: "/audio/voices", Items: "$.voices", ID: "$.id", Name: "$.name"}
}

// .
// .
// .
// .
// .
// .
// .
// .
func (a *App) addShippedSpeechService(name, apiKey string) error {
	var shipped *providerEntry
	for _, e := range embeddedRegistry().Providers {
		if e.Name == name && e.Speech != nil {
			shipped = &e
			break
		}
	}
	if shipped == nil {
		return fmt.Errorf("%q is not a speech service this release ships", name)
	}
	reg, err := a.loadProviders()
	if err != nil {
		return err
	}
	if entryNamed(reg, name) != nil {
		return fmt.Errorf("%s is already in providers.json; its key is edited under Providers", name)
	}
	// .
	raw, err := json.Marshal(shipped)
	if err != nil {
		return err
	}
	var e providerEntry
	if err := json.Unmarshal(raw, &e); err != nil {
		return err
	}
	e.Speech, e.EffortLevels, e.CatalogueAuthor = nil, nil, ""
	e.APIKey = strings.TrimSpace(apiKey)
	return a.setProvider(e, false)
}
