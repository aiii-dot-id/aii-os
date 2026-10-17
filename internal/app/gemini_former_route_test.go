package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
)

// formerGeminiFile is the Google Gemini entry as the 0.1.17 seed wrote it
// into every identity's file, with a key the operator typed since.
const formerGeminiEntry = `{"name":"Google Gemini","catalogue_author":"google","api_type":"openai",` +
	`"effort_levels":["none","minimal","low","medium","high","xhigh"],` +
	`"url":"https://generativelanguage.googleapis.com/v1beta/openai","api_key":"typed-key",` +
	`"subscribe_url":"https://ai.google.dev","default_model":"gemini-3.1-pro-preview",` +
	`"models":["gemini-3.1-pro-preview","gemini-3.7-flash"]}`

func writeProvidersJSON(t *testing.T, entries ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "providers.json")
	if err := os.WriteFile(path, []byte(`{"providers":[`+strings.Join(entries, ",")+`]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// THE MOVE. The entry an earlier seed wrote resolves to Google's own API
// with that API's limits, and the request the client would make is the
// native one. Saved again, the file states what it stated: the lend is
// for the run, a release rolled back finds its own file, and nothing of
// the shipped registry's own (where the entry formerly pointed) is
// written into it.
func TestAnEntryOnTheFormerSeedsRouteIsServedByTheRouteShippedNow(t *testing.T) {
	path := writeProvidersJSON(t, formerGeminiEntry)
	reg, err := loadProvidersFile(path)
	if err != nil {
		t.Fatal(err)
	}
	e := entryNamed(reg, "Google Gemini")
	if e == nil {
		t.Fatal("the entry was not admitted")
	}
	if e.APIType != "gemini" || e.URL != "https://generativelanguage.googleapis.com/v1beta" || e.ContextLength != 1048576 || e.MaxOutputTokens != 65536 || e.APIKey != "typed-key" {
		t.Fatalf("the entry loads as dialect %q at %q, window %d, output %d", e.APIType, e.URL, e.ContextLength, e.MaxOutputTokens)
	}
	a := New(&Config{LLM: LLMConfig{Provider: "Google Gemini"}, SourcePath: filepath.Join(filepath.Dir(path), "config.json")})
	cc, _, err := a.resolveLLM()
	if err != nil {
		t.Fatal(err)
	}
	if cc.Provider != "gemini" || cc.Endpoint != "https://generativelanguage.googleapis.com/v1beta" || cc.MaxOutputTokens != 65536 || cc.APIKey != "typed-key" || cc.ThinkingMode != "level" {
		t.Fatalf("the client is built for dialect %q at %q, output %d, thinking %q", cc.Provider, cc.Endpoint, cc.MaxOutputTokens, cc.ThinkingMode)
	}

	if _, err := saveProvidersFile(path, reg); err != nil {
		t.Fatal(err)
	}
	var saved struct {
		Providers []map[string]any `json:"providers"`
	}
	raw, _ := os.ReadFile(path)
	if err := json.Unmarshal(raw, &saved); err != nil || len(saved.Providers) != 1 {
		t.Fatalf("the saved file: %v\n%s", err, raw)
	}
	got := saved.Providers[0]
	if got["api_type"] != "openai" || got["url"] != "https://generativelanguage.googleapis.com/v1beta/openai" {
		t.Errorf("a save rewrote the route the file states: api_type %v, url %v", got["api_type"], got["url"])
	}
	for _, member := range []string{"context_length", "max_output_tokens", "formerly"} {
		if _, written := got[member]; written {
			t.Errorf("a save wrote %q into the operator's file: %v", member, got[member])
		}
	}
	if again, err := loadProvidersFile(path); err != nil || entryNamed(again, "Google Gemini").APIType != "gemini" {
		t.Fatalf("the saved file does not load as it did: %v", err)
	}
}

// ONLY THE FORMER SEED'S OWN BYTES ARE MOVED. An address the operator
// typed, an entry of another name, one that adopts a credential, and a
// window the operator set are theirs and stand.
func TestOnlyAnUntouchedFormerRouteIsMoved(t *testing.T) {
	with := func(old, new string) string {
		if !strings.Contains(formerGeminiEntry, old) {
			t.Fatalf("the fixture has no %q", old)
		}
		return strings.Replace(formerGeminiEntry, old, new, 1)
	}
	for name, tc := range map[string]struct {
		entry    string
		wantType string
		window   int
	}{
		"an address the operator typed":     {with(`/v1beta/openai"`, `/v1beta/openai/"`), "openai", 0},
		"another address":                   {with(`https://generativelanguage.googleapis.com/v1beta/openai`, `https://gateway.example/v1`), "openai", 0},
		"an entry of the operator's own":    {with(`"name":"Google Gemini"`, `"name":"My Gemini"`), "openai", 0},
		"an entry that adopts a credential": {with(`"api_key":"typed-key"`, `"credential":"file:/nowhere/token.json"`), "openai", 0},
		"a window the operator set":         {with(`"api_key":"typed-key"`, `"context_length":200000`), "gemini", 200000},
	} {
		t.Run(name, func(t *testing.T) {
			path := writeProvidersJSON(t, tc.entry)
			reg, err := loadProvidersFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if len(reg.Providers) != 1 {
				t.Fatalf("the entry was not admitted: %+v", reg.broken)
			}
			e := reg.Providers[0]
			if e.APIType != tc.wantType || (tc.window != 0 && e.ContextLength != tc.window) || (tc.wantType == "openai" && e.ContextLength != 0) {
				t.Fatalf("loaded as dialect %q at %q with window %d", e.APIType, e.URL, e.ContextLength)
			}
			if _, err := saveProvidersFile(path, reg); err != nil {
				t.Fatal(err)
			}
			if raw, _ := os.ReadFile(path); tc.window != 0 && (!strings.Contains(string(raw), `"context_length": 200000`) || strings.Contains(string(raw), "max_output_tokens")) {
				t.Fatalf("the operator's window was not kept, or the lent allocation was written:\n%s", raw)
			}
		})
	}
}

func TestAMovedEntryThatStatesACacheSettingStillStarts(t *testing.T) {
	for name, cache := range map[string]string{"a routing key": `{"key":"abc"}`, "a retention": `{"ttl":"24h"}`} {
		t.Run(name, func(t *testing.T) {
			path := writeProvidersJSON(t, strings.Replace(formerGeminiEntry, `"api_key":"typed-key",`, `"api_key":"typed-key","cache":`+cache+`,`, 1))
			logs := logsink.CaptureForTest(t)
			a := New(&Config{LLM: LLMConfig{Provider: "Google Gemini"}, SourcePath: filepath.Join(filepath.Dir(path), "config.json")})
			cc, _, err := a.resolveLLM()
			if err != nil || cc.Provider != "gemini" || cc.Cache != nil {
				t.Fatalf("the start: dialect %q, cache %+v, %v", cc.Provider, cc.Cache, err)
			}
			said := logs.String()
			for _, want := range []string{`api_type "openai" at https://generativelanguage.googleapis.com/v1beta/openai`, `api_type "gemini" at https://generativelanguage.googleapis.com/v1beta `, "the file is left as it is", "1048576 and 65536", "cache setting is not carried"} {
				if !strings.Contains(said, want) {
					t.Errorf("the start does not say %q:\n%s", want, said)
				}
			}
			reg, err := loadProvidersFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := saveProvidersFile(path, reg); err != nil {
				t.Fatal(err)
			}
			if raw, _ := os.ReadFile(path); !strings.Contains(strings.Join(strings.Fields(string(raw)), ""), `"cache":`+cache) {
				t.Errorf("a save lost the cache setting the file states:\n%s", raw)
			}
		})
	}
	// An entry on its own route says nothing at a start, and one whose
	// file states the shipped route is not said to be moved.
	path := writeProvidersJSON(t, strings.NewReplacer(`"api_type":"openai"`, `"api_type":"gemini"`, `/v1beta/openai"`, `/v1beta"`).Replace(formerGeminiEntry))
	logs := logsink.CaptureForTest(t)
	if _, _, err := New(&Config{LLM: LLMConfig{Provider: "Google Gemini"}, SourcePath: filepath.Join(filepath.Dir(path), "config.json")}).resolveLLM(); err != nil || strings.Contains(logs.String(), "earlier release") {
		t.Errorf("an entry on the shipped route: %v; said %q", err, logs.String())
	}
}

// A CHANGE ON THE CARD IS THE OPERATOR'S. The card shows the route the
// entry is served by; a save that leaves it as shown leaves the file as
// it was, and a save that moves the entry elsewhere writes where it was
// moved, without the window that was lent for Google's models.
func TestACardSaveOfAMovedEntryWritesOnlyWhatTheOperatorChanged(t *testing.T) {
	path := writeProvidersJSON(t, formerGeminiEntry)
	a := New(&Config{SourcePath: filepath.Join(filepath.Dir(path), "config.json")})
	card := func() dashboard.ProviderInfo {
		for _, p := range a.providerDirectory().Providers {
			if p.Name == "Google Gemini" {
				return p
			}
		}
		t.Fatal("no Google Gemini card")
		return dashboard.ProviderInfo{}
	}
	shown := card()
	if shown.APIType != "gemini" || shown.Endpoint != "https://generativelanguage.googleapis.com/v1beta" || shown.ContextLength != 1048576 {
		t.Fatalf("the card shows dialect %q at %q, window %d", shown.APIType, shown.Endpoint, shown.ContextLength)
	}
	// As the card sends it back with one field changed.
	shown.ConfiguredModels, shown.DefaultModel = []string{"gemini-3.8-flash"}, "gemini-3.8-flash"
	if err := a.setProviderInfo(dashboard.ProviderEdit{ProviderInfo: shown}); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	if !strings.Contains(string(raw), `"api_type": "openai"`) || !strings.Contains(string(raw), `"url": "https://generativelanguage.googleapis.com/v1beta/openai"`) ||
		!strings.Contains(string(raw), `"default_model": "gemini-3.8-flash"`) || strings.Contains(string(raw), "context_length") || !strings.Contains(string(raw), `"api_key": "typed-key"`) {
		t.Fatalf("a save of the card as shown, with a new default model, wrote:\n%s", raw)
	}
	// Moved to an address of the operator's own, with the key entered again
	// for it: a stored key does not follow its entry to another address
	// (movedKeyRefusal).
	shown = card()
	shown.Endpoint = "https://gateway.example/v1beta"
	if err := a.setProviderInfo(dashboard.ProviderEdit{ProviderInfo: shown, APIKey: "typed-key"}); err != nil {
		t.Fatal(err)
	}
	raw, _ = os.ReadFile(path)
	if !strings.Contains(string(raw), `"api_type": "gemini"`) || !strings.Contains(string(raw), `"url": "https://gateway.example/v1beta"`) || strings.Contains(string(raw), "context_length") {
		t.Fatalf("a save that moved the entry wrote:\n%s", raw)
	}
}

// A new identity's file is written from the seed as it is now, and does
// not carry where the entry formerly pointed.
func TestAScaffoldedFileStatesTheNativeRouteAndNotTheFormerOne(t *testing.T) {
	var file struct {
		Providers []map[string]any `json:"providers"`
	}
	if err := json.Unmarshal(scaffoldProviders(), &file); err != nil {
		t.Fatal(err)
	}
	for _, e := range file.Providers {
		if _, written := e["formerly"]; written {
			t.Errorf("the scaffold wrote %v's former route into the operator's file", e["name"])
		}
		if e["name"] == "Google Gemini" && (e["api_type"] != "gemini" || e["url"] != "https://generativelanguage.googleapis.com/v1beta") {
			t.Errorf("a new file's Gemini entry: api_type %v at %v", e["api_type"], e["url"])
		}
	}
	if len(shippedGemini(t).Formerly) != 1 {
		t.Error("the shipped entry does not say where it formerly pointed")
	}
}
