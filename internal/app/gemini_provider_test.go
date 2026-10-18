package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/llm"
)

// gemini_provider_test.go — the shipped Gemini entry speaks Google's own
// API, and everything an identity is configured with reaches it: the
// model's window and output limit (which bounds thinking and answer
// together, so the 8192 an undeclared entry gets cuts replies off), the
// levels each model takes, and a request the API reads.

func shippedGemini(t *testing.T) providerEntry {
	t.Helper()
	var reg providerRegistry
	if err := json.Unmarshal(embeddedProviders, &reg); err != nil {
		t.Fatal(err)
	}
	for _, e := range reg.Providers {
		if e.Name == "Google Gemini" {
			return e
		}
	}
	t.Fatal("the embedded registry has no Google Gemini entry")
	return providerEntry{}
}

// The shipped entry offers what Google serves: models that exist, each
// with a row that says which thinking levels it takes (the API answers a
// level a model does not take with an error), and the limits Google
// publishes for every one of them. The output limit is not a nicety: on
// Gemini it bounds thinking and answer together, so the 8192 an entry
// that declares nothing is given cuts replies off mid-thought.
func TestTheGeminiEntryOffersWhatGoogleServes(t *testing.T) {
	var reg providerRegistry
	if err := json.Unmarshal(embeddedProviders, &reg); err != nil {
		t.Fatal(err)
	}
	e := shippedGemini(t)
	if e.VendorKeyEnv != "GEMINI_API_KEY" {
		t.Errorf("the key variable is %q", e.VendorKeyEnv)
	}
	if e.ContextLength != 1048576 || e.MaxOutputTokens != 65536 {
		t.Errorf("the entry declares a window of %d and an output allocation of %d; Google publishes 1,048,576 and 65,536 for each of its models", e.ContextLength, e.MaxOutputTokens)
	}
	if !slices.Contains(e.Models, e.DefaultModel) {
		t.Errorf("the default model %q is not among the entry's own %v", e.DefaultModel, e.Models)
	}

	if e.DefaultModel != "gemini-3.8-flash" {
		t.Errorf("the default model is %q", e.DefaultModel)
	}
	if slices.Contains(e.Models, "gemini-3.7-flash") {
		t.Error("the entry still offers gemini-3.7-flash, which Google routes to gemini-3.8-flash")
	}
	for _, m := range e.Models {
		row, ok := reg.ModelCapabilities[m]
		if !ok || len(row.Effort) == 0 {
			t.Errorf("model %q has no row naming its thinking levels", m)
		}
		for _, level := range row.Effort {
			if !slices.Contains(e.EffortLevels, level) {
				t.Errorf("model %q takes %q, which the entry's own vocabulary %v does not offer", m, level, e.EffortLevels)
			}
		}
	}
	if slices.Contains(e.EffortLevels, "none") || slices.Contains(e.EffortLevels, "xhigh") {
		t.Errorf("the entry offers %v; a Gemini 3 model takes minimal, low, medium and high, and cannot be told not to think", e.EffortLevels)
	}
	if e.Speech == nil {
		t.Error("the entry lost its speech block")
	}
}

// The shipped entry is the native route: the API's own base and the
// gemini dialect.
func TestEmbeddedGeminiUsesGooglesOwnAPI(t *testing.T) {
	e := shippedGemini(t)
	if e.APIType != "gemini" || e.URL != "https://generativelanguage.googleapis.com/v1beta" || entryDialect(e) != llm.DialectGemini || e.Credential != "" {
		t.Errorf("the route is dialect %q at %q, credential %q", e.APIType, e.URL, e.Credential)
	}
	// Each model the entry lists is said to think, in the dialect's own
	// word: that is what lets readable reasoning be asked of it.
	var reg providerRegistry
	if err := json.Unmarshal(embeddedProviders, &reg); err != nil {
		t.Fatal(err)
	}
	for _, m := range e.Models {
		if got := reg.ModelCapabilities[m].Thinking; got != "level" {
			t.Errorf("model %q: thinking shape %q, want level", m, got)
		}
	}
}

// What the entry resolves to is what a request carries. The level asked
// for is held to the model's own row: gemini-3.8-flash takes no
// "minimal", so it is not sent there and is sent to a model that takes
// it. A model with no row is one nobody has said thinks, and the API
// answers thinkingConfig with an error on a model that cannot: it is sent
// none, so the request nobody configured is one no model refuses.
func TestTheGeminiEntryReachesTheWireAsConfigured(t *testing.T) {
	var path, key string
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, key = r.URL.RequestURI(), r.Header.Get("x-goog-api-key")
		raw, _ := io.ReadAll(r.Body)
		body = nil
		_ = json.Unmarshal(raw, &body)
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, `data: {"candidates":[{"content":{"parts":[{"text":"ok"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":3,"candidatesTokenCount":1,"totalTokenCount":4}}`+"\n\n")
	}))
	defer srv.Close()

	for _, tc := range []struct {
		model, effort string
		wantLevel     any
		wantThoughts  any
	}{
		{"gemini-3.8-flash", "minimal", nil, true},
		{"gemini-3.8-flash", "high", "HIGH", true},
		{"gemini-3.6-flash", "minimal", "MINIMAL", true},
		{"gemini-3.1-pro-preview", "", nil, true},
		{"a-model-with-no-row", "", nil, nil},
	} {
		t.Run(tc.model+"/"+tc.effort, func(t *testing.T) {
			dir := t.TempDir()
			entry := shippedGemini(t)
			entry.URL, entry.APIKey, entry.ReasoningEffort = srv.URL+"/v1beta", "the-key", tc.effort
			entry.DefaultModel, entry.Models, entry.Speech = tc.model, []string{tc.model}, nil
			writeTestProviders(t, dir, entry)
			a := resolverApp(dir, LLMConfig{Provider: entry.Name})
			cc, _, err := a.resolveLLM()
			if err != nil {
				t.Fatal(err)
			}
			if cc.Provider != "gemini" || cc.MaxOutputTokens != 65536 || cc.Model != tc.model {
				t.Fatalf("resolved to dialect %q, model %q, output %d", cc.Provider, cc.Model, cc.MaxOutputTokens)
			}
			resp, err := llm.New(&cc).Chat(context.Background(), []llm.Message{{Role: "user", Content: "hi"}}, llm.ChatOptions{})
			if err != nil || resp.Choices[0].Message.Content != "ok" || resp.Usage.TotalTokens != 4 {
				t.Fatalf("chat: %+v, %v", resp, err)
			}
			if path != "/v1beta/models/"+tc.model+":streamGenerateContent?alt=sse" || key != "the-key" {
				t.Errorf("the request went to %s with key %q", path, key)
			}
			gen, _ := body["generationConfig"].(map[string]any)
			thinking, _ := gen["thinkingConfig"].(map[string]any)
			if gen["maxOutputTokens"] != 65536.0 || thinking["thinkingLevel"] != tc.wantLevel || thinking["includeThoughts"] != tc.wantThoughts {
				t.Errorf("generationConfig = %v", gen)
			}
			if _, sent := gen["thinkingConfig"]; sent != (tc.wantThoughts != nil) {
				t.Errorf("thinkingConfig sent: %t; generationConfig = %v", sent, gen)
			}
		})
	}
}

// The key for a Gemini entry is read from Google's own variable when the
// entry carries none, and that variable is sent to no other provider.
func TestTheGeminiKeyVariableIsReadForTheGeminiEntry(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "from-the-environment")
	dir := t.TempDir()
	entry := shippedGemini(t)
	entry.Speech = nil
	other := providerEntry{Name: "elsewhere", APIType: "openai", URL: "https://elsewhere.example/v1", DefaultModel: "m"}
	writeTestProviders(t, dir, entry, other)
	cc, _, err := resolverApp(dir, LLMConfig{Provider: entry.Name}).resolveLLM()
	if err != nil || cc.APIKey != "from-the-environment" {
		t.Fatalf("the Gemini entry's key = %q, %v", cc.APIKey, err)
	}
	cc, _, err = resolverApp(dir, LLMConfig{Provider: other.Name}).resolveLLM()
	if err != nil || cc.APIKey != "" {
		t.Fatalf("Google's variable reached another provider: key %q, %v", cc.APIKey, err)
	}
}

// The Gemini API's model list: its own path and key header, a page large
// enough to be the whole list, rows named as resources, each with its
// limits and its methods. A model that cannot generate content is not
// offered to think with, and a key the API refuses (a 400 that names it)
// is the credential's failure, not an unreachable provider.
func TestGeminiModelDiscovery(t *testing.T) {
	const list = `{"models":[` +
		`{"name":"models/gemini-3.8-flash","inputTokenLimit":1048576,"outputTokenLimit":65536,"supportedGenerationMethods":["generateContent","countTokens"],"thinking":true},` +
		`{"name":"models/gemini-embedding-2","inputTokenLimit":8192,"supportedGenerationMethods":["embedContent"]},` +
		`{"name":"models/veo-3.1-generate-preview","supportedGenerationMethods":["predictLongRunning"]},` +
		`{"name":"models/gemini-3.1-pro-preview","inputTokenLimit":1048576,"outputTokenLimit":65536,"supportedGenerationMethods":["generateContent"]}]}`
	var uri string
	var header http.Header
	status, reply := http.StatusOK, list
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		uri, header = r.URL.RequestURI(), r.Header.Clone()
		w.WriteHeader(status)
		io.WriteString(w, reply)
	}))
	defer srv.Close()

	models, meta, err := discoverModelsWith(context.Background(), "gemini", srv.URL+"/v1beta", "the-key", false, nil, nil)
	if err != nil || !reflect.DeepEqual(models, []string{"gemini-3.8-flash", "gemini-3.1-pro-preview"}) {
		t.Fatalf("models = %v, %v", models, err)
	}
	if want := (modelMeta{Context: 1048576, MaxOut: 65536}); meta["gemini-3.8-flash"] != want || meta["gemini-3.1-pro-preview"] != want {
		t.Errorf("limits = %+v", meta)
	}
	if uri != "/v1beta/models?pageSize=1000" || header.Get("x-goog-api-key") != "the-key" || header.Get("Authorization") != "" {
		t.Errorf("asked %s with headers %v", uri, header)
	}

	if _, _, err := discoverModelsWith(context.Background(), "gemini", srv.URL+"/v1beta", "token", true, nil, nil); err != nil || header.Get("Authorization") != "Bearer token" || header.Get("x-goog-api-key") != "" {
		t.Errorf("a bearer credential: %v, headers %v", err, header)
	}

	status, reply = http.StatusBadRequest, `{"error":{"code":400,"message":"API key not valid. Please pass a valid API key.","status":"INVALID_ARGUMENT","details":[{"@type":"type.googleapis.com/google.rpc.ErrorInfo","reason":"API_KEY_INVALID"}]}}`
	if _, _, err := discoverModelsWith(context.Background(), "gemini", srv.URL+"/v1beta", "bad", false, nil, nil); !errors.Is(err, errCredentialRejected) {
		t.Errorf("a refused key reads as %v, want the credential's rejection", err)
	}
	reply = `{"error":{"code":400,"message":"Invalid page size.","status":"INVALID_ARGUMENT"}}`
	if _, _, err := discoverModelsWith(context.Background(), "gemini", srv.URL+"/v1beta", "good", false, nil, nil); err == nil || errors.Is(err, errCredentialRejected) || errors.Is(err, errAuthRequired) {
		t.Errorf("another 400 reads as %v, want the status and nothing about the credential", err)
	}
	status, reply = http.StatusForbidden, `{"error":{"code":403,"message":"Method doesn't allow unregistered callers.","status":"PERMISSION_DENIED"}}`
	if _, _, err := discoverModelsWith(context.Background(), "gemini", srv.URL+"/v1beta", "", false, nil, nil); !errors.Is(err, errAuthRequired) {
		t.Errorf("no key reads as %v, want the ask for one", err)
	}
}

// The card of a Gemini entry offers what a request can carry: readable
// reasoning in the member the API names, the levels of the model in use,
// and no cache setting, because the API takes none.
func TestTheGeminiCardOffersWhatARequestCarries(t *testing.T) {
	entry := shippedGemini(t)
	entry.URL, entry.Speech = "http://127.0.0.1:1/v1beta", nil // nothing answers: the card's probe reaches nobody
	entry.DefaultModel = "gemini-3.8-flash"
	infos := firstbootApp(t, t.TempDir()).providerInfos(&providerRegistry{Providers: []providerEntry{entry}})
	if len(infos) != 1 {
		t.Fatalf("one card expected, got %d", len(infos))
	}
	card := infos[0]
	if card.APIType != "gemini" || card.SummaryField != "generationConfig.thinkingConfig.includeThoughts" || !reflect.DeepEqual(card.SummaryLevels, []string{"summarized"}) || card.ThinkingDisplay != "summarized" {
		t.Errorf("readable reasoning on the card: type %q field %q levels %v shown %q", card.APIType, card.SummaryField, card.SummaryLevels, card.ThinkingDisplay)
	}
	if !reflect.DeepEqual(card.EffortLevels, []string{"low", "medium", "high"}) {
		t.Errorf("gemini-3.8-flash is offered %v, want the three levels it takes", card.EffortLevels)
	}
	if !reflect.DeepEqual(card.CacheModes, []string{"auto"}) || card.CacheTTLs != nil || card.CacheKeySupported || card.CacheDiagnostics {
		t.Errorf("cache on the card: modes %v ttls %v key %t diagnostics %t", card.CacheModes, card.CacheTTLs, card.CacheKeySupported, card.CacheDiagnostics)
	}
	if card.ContextLength != 1048576 || card.MaxOutputTokens != 65536 {
		t.Errorf("limits on the card: %d, %d", card.ContextLength, card.MaxOutputTokens)
	}
	// An entry of the operator's own on this dialect gets the dialect's
	// floor: the levels every Gemini model takes.
	reg := &providerRegistry{Providers: []providerEntry{{Name: "my gemini", APIType: "gemini", URL: "http://127.0.0.1:1/v1beta", DefaultModel: "gemini-next"}}}
	fillEmbeddedEffortLevels(reg)
	if got := reg.Providers[0].EffortLevels; !reflect.DeepEqual(got, []string{"low", "medium", "high"}) {
		t.Errorf("an uncatalogued Gemini model is offered %v", got)
	}
}

// An api_type the registry does not know is refused by name, and gemini
// is one it knows.
func TestProviderAPITypeAdmitsGemini(t *testing.T) {
	e := providerEntry{Name: "g", APIType: "gemini"}
	if err := normalizeProviderAPIType(&e); err != nil {
		t.Fatal(err)
	}
	e.APIType = "vertex"
	if err := normalizeProviderAPIType(&e); err == nil || !strings.Contains(err.Error(), "openai, anthropic or gemini") {
		t.Fatalf("an unknown api_type: %v", err)
	}
}
