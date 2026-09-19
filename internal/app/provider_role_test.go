package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
)

// .
// .
// .
// .
// .
// .
// .
func TestAProviderIsChatOrSpeechOnlyAndTheFileSaysWhich(t *testing.T) {
	yes, no := true, false
	cases := []struct {
		name string
		e    providerEntry
		chat bool
	}{
		{"inferred chat: models, no speech", providerEntry{Name: "x", Models: []string{"m"}}, true},
		{"inferred speech-only: speech, no models", providerEntry{Name: "x", Speech: &speechBlock{}}, false},
		{"declared speech-only wins over its models", providerEntry{Name: "x", Chat: &no, Speech: &speechBlock{}, Models: []string{"m"}}, false},
		{"declared chat wins over the inference", providerEntry{Name: "x", Chat: &yes, Speech: &speechBlock{}}, true},
		// .
		// .
		// .
		// .
		// .
		{"a shipped chat vendor with a lent block and no models still chats", providerEntry{Name: "OpenAI", Speech: &speechBlock{}}, true},
		{"a shipped speech vendor undeclared in an older file is speech-only", providerEntry{Name: "ElevenLabs", Speech: &speechBlock{}}, false},
	}
	for _, c := range cases {
		if got := chatProvider(c.e); got != c.chat {
			t.Errorf("%s: chat=%v, want %v", c.name, got, c.chat)
		}
	}
	// .
	// .
	for _, e := range embeddedRegistry().Providers {
		declared := e.Chat != nil && !*e.Chat
		if speechOnly(e) != declared {
			t.Errorf("shipped %q: speech-only by inference %v, declared chat: false %v — the file must say it", e.Name, speechOnly(e), declared)
		}
	}
}

// .
// .
func TestAnEntryThatNeitherChatsNorSpeaksIsRefused(t *testing.T) {
	a := newVoiceApp(t)
	path := filepath.Join(filepath.Dir(a.cfg.SourcePath), "providers.json")
	if err := os.WriteFile(path, []byte(`{"providers":[{"name":"nothing","url":"https://nothing.test","chat":false}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	reg, err := a.loadProviders()
	if err != nil || len(reg.Providers) != 0 || len(reg.broken) != 1 || !strings.Contains(reg.broken[0].reason, `"nothing" says chat: false`) {
		t.Fatalf("an entry with nothing to serve was admitted, or not set aside with its reason: %v %+v", err, reg)
	}
	if reg.broken[0].repair != nil {
		t.Fatalf("a repair was offered that would change what the entry is for: %+v", reg.broken[0].repair)
	}
}

// .
// .
func TestTheOperatorsOwnSpeechServerIsDeclaredSpeechOnly(t *testing.T) {
	a := newVoiceApp(t)
	if err := a.setOwnSpeechServer("my-speech", "k-1", "http://127.0.0.1:9/v1"); err != nil {
		t.Fatal(err)
	}
	reg, err := a.loadProviders()
	if err != nil {
		t.Fatal(err)
	}
	e := entryNamed(reg, "my-speech")
	if e == nil || e.Chat == nil || *e.Chat || chatProvider(*e) {
		t.Fatalf("the own speech server is not declared speech-only: %+v", e)
	}
	// .
	// .
	// .
	e.Chat = nil
	if _, err := saveProvidersFile(a.providersPath(), reg); err != nil {
		t.Fatal(err)
	}
	if err := a.setOwnSpeechServer("my-speech", "", "http://127.0.0.1:10/v1"); err != nil {
		t.Fatal(err)
	}
	if reg, err = a.loadProviders(); err != nil {
		t.Fatal(err)
	}
	if e = entryNamed(reg, "my-speech"); e == nil || e.Chat == nil || *e.Chat || e.URL != "http://127.0.0.1:10/v1" {
		t.Fatalf("a repointed own speech server is not declared speech-only: %+v", e)
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
func TestASaveKeepsTheStoredRoleUnlessItDeclaresChat(t *testing.T) {
	a := newVoiceApp(t)
	path := filepath.Join(filepath.Dir(a.cfg.SourcePath), "providers.json")
	if err := os.WriteFile(path, []byte(`{"providers":[{"name":"ElevenLabs","url":"https://api.elevenlabs.io","chat":false}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := a.setProviderInfo(dashboard.ProviderInfo{Name: "ElevenLabs", Endpoint: "https://api.elevenlabs.io", DefaultModel: "eleven-chat"}); err != nil {
		t.Fatal(err)
	}
	reg, err := a.loadProviders()
	if err != nil {
		t.Fatal(err)
	}
	if e := entryNamed(reg, "ElevenLabs"); e == nil || e.Chat == nil || *e.Chat || chatProvider(*e) {
		t.Fatalf("a save that said nothing about the role erased the declaration: %+v", e)
	}
	if raw, _ := os.ReadFile(path); !strings.Contains(string(raw), `"chat": false`) {
		t.Fatalf("the file no longer says chat: false:\n%s", raw)
	}
	if err := a.setProviderInfo(dashboard.ProviderInfo{Name: "ElevenLabs", Endpoint: "https://api.elevenlabs.io", DefaultModel: "eleven-chat", Chat: true}); err != nil {
		t.Fatal(err)
	}
	if reg, err = a.loadProviders(); err != nil {
		t.Fatal(err)
	}
	if e := entryNamed(reg, "ElevenLabs"); e == nil || e.Chat == nil || !*e.Chat || !chatProvider(*e) {
		t.Fatalf("a save from the chat card did not declare chat: %+v", e)
	}
	if raw, _ := os.ReadFile(path); !strings.Contains(string(raw), `"chat": true`) {
		t.Fatalf("the file does not say chat: true:\n%s", raw)
	}
	// .
	// .
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	name, err := a.upsertBirthProvider(&dashboard.GenesisRequest{Endpoint: "https://api.mistral.ai/v1", Model: "mistral-large", APIKey: "k"})
	if err != nil || name != "Mistral" {
		t.Fatalf("birth did not adopt the shipped entry at that address: %q, %v", name, err)
	}
	if reg, err = a.loadProviders(); err != nil {
		t.Fatal(err)
	}
	if e := entryNamed(reg, "Mistral"); e == nil || !e.Default || e.Chat == nil || !*e.Chat || !chatProvider(*e) {
		t.Fatalf("the entry a birth adopted is not declared chat: %+v", e)
	}
	if _, _, err := a.resolveLLMConfig(LLMConfig{Provider: "Mistral"}, reg); err != nil {
		t.Fatalf("the born substrate does not resolve: %v", err)
	}
}

// .
// .
// .
// .
// .
func TestASpeechOnlyEntryIsNeverTheSubstrate(t *testing.T) {
	no := false
	reg := &providerRegistry{Providers: []providerEntry{
		{Name: "chat", APIType: "openai", URL: "https://chat.test", Models: []string{"m"}, DefaultModel: "m"},
		{Name: "voice", APIType: "openai", URL: "https://voice.test", Chat: &no, Speech: &speechBlock{}, DefaultModel: "m"},
		// .
		// .
		// .
		{Name: "OpenAI", APIType: "openai", URL: "https://api.openai.com/v1", Speech: &speechBlock{}},
	}}
	a := newVoiceApp(t)
	if _, _, err := a.resolveLLMConfig(LLMConfig{Provider: "voice"}, reg); err == nil || !strings.Contains(err.Error(), `"voice" serves speech only`) {
		t.Fatalf("a speech-only entry with a default_model resolved as the substrate: %v", err)
	}
	if _, _, err := a.resolveLLMConfig(LLMConfig{Provider: "chat"}, reg); err != nil {
		t.Fatalf("the chat provider did not resolve: %v", err)
	}
	if _, _, err := a.resolveLLMConfig(LLMConfig{Provider: "OpenAI", Model: "gpt"}, reg); err != nil {
		t.Fatalf("a shipped chat vendor with a lent speech block and no models did not resolve: %v", err)
	}
	path := filepath.Join(filepath.Dir(a.cfg.SourcePath), "providers.json")
	if err := os.WriteFile(path, []byte(`{"providers":[{"name":"ElevenLabs","url":"https://api.elevenlabs.io","chat":false,"default":true}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := a.loadProviders()
	if err != nil || len(loaded.broken) != 1 || !strings.Contains(loaded.broken[0].reason, `"ElevenLabs" is speech-only but flagged default`) {
		t.Fatalf("a speech-only default was admitted, or not set aside with its reason: %v %+v", err, loaded)
	}
	if r := loaded.broken[0].repair; r == nil || r.what != "clear its default flag" {
		t.Fatalf("the repair for a speech-only default is not its flag: %+v", r)
	}
	if err := os.WriteFile(path, []byte(`{"providers":[{"name":"ElevenLabs","url":"https://api.elevenlabs.io","chat":false}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	err = a.setProviderInfo(dashboard.ProviderInfo{Name: "ElevenLabs", Endpoint: "https://api.elevenlabs.io", Default: true})
	if err == nil || !strings.Contains(err.Error(), `"ElevenLabs" is speech-only but flagged default`) {
		t.Fatalf("the form wrote a speech-only default: %v", err)
	}
	if _, err := a.loadProviders(); err != nil {
		t.Fatalf("the refused edit reached the file: %v", err)
	}
}
