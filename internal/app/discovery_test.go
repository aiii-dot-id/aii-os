package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/logsink"
)

func TestDiscoverModelsRejectsOversizeResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(bytes.Repeat([]byte{'x'}, maxModelListBytes+1))
	}))
	t.Cleanup(server.Close)

	_, _, err := discoverModelsWith(context.Background(), "", server.URL, "", false, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("oversize model list returned %v", err)
	}
}

func TestProviderDirectoryAddsDiscoveredModels(t *testing.T) {
	dir := t.TempDir()
	entry := providerEntry{
		Name: "provider", APIType: "openai", URL: "https://provider.example/v1",
		DefaultModel: "configured", Models: []string{"configured", "fallback"},
	}
	writeTestProviders(t, dir, entry)
	a := New(&Config{SourcePath: filepath.Join(dir, "config.json")})
	a.provStatus = map[string]providerProbe{entry.Name: {
		state: "ok", models: []string{"live-b", "live-a"},
		checkedAt: time.Now(), key: probeKey(entry),
	}}

	providers := a.providerDirectoryLive()
	if len(providers) != 1 {
		t.Fatalf("got %d providers, want 1", len(providers))
	}
	if want := []string{"configured", "fallback", "live-b", "live-a"}; !reflect.DeepEqual(providers[0].Models, want) {
		t.Fatalf("picker models = %v, want %v", providers[0].Models, want)
	}
	if want := entry.Models; !reflect.DeepEqual(providers[0].ConfiguredModels, want) {
		t.Fatalf("editor fallback = %v, want %v", providers[0].ConfiguredModels, want)
	}
}

func TestProbeKeyCoversProviderEdits(t *testing.T) {
	entry := providerEntry{Name: "same", URL: "https://example.com", APIType: "openai", APIKey: "first"}
	before := probeKey(entry)
	entry.APIKey = "second"
	if after := probeKey(entry); after == before {
		t.Fatal("credential edit retained a stale provider-probe cache key")
	}
	if strings.Contains(before, "first") {
		t.Fatal("provider-probe cache key copied the credential")
	}
}

func TestDiscoverForProviderAddsConfiguredModels(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"id":"shared"},{"id":"live"}]}`))
	}))
	t.Cleanup(server.Close)

	entry := providerEntry{Name: "provider", URL: server.URL, Models: []string{"configured", "shared"}}
	a := New(&Config{})
	models, err := a.discoverForProvider(context.Background(), &providerRegistry{Providers: []providerEntry{entry}}, entry.Name, "")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"configured", "shared", "live"}; !reflect.DeepEqual(models, want) {
		t.Fatalf("models = %v, want %v", models, want)
	}
}

// .
// .
// .
func TestAPIVersionInPath(t *testing.T) {
	for _, tc := range []struct {
		url  string
		want bool
	}{
		{"https://api.anthropic.com", false},
		{"https://api.anthropic.com/v1", true},
		{"https://api.openai.com/v1", true},
		{"https://generativelanguage.googleapis.com/v1beta/openai", true},
		{"https://chatgpt.com/backend-api/codex", false},
		{"https://api.z.ai/api/paas/v4", true},
		{"https://example.com/version/api", false},
	} {
		if got := apiVersionInPath(tc.url); got != tc.want {
			t.Errorf("%s -> %v, want %v", tc.url, got, tc.want)
		}
	}
}

// .
// .
func TestParseModelListShapesAndWindows(t *testing.T) {
	anthropic := `{"data":[{"id":"claude-opus-5","max_input_tokens":1000000,"max_tokens":128000}]}`
	models, meta, err := parseModelList([]byte(anthropic))
	if err != nil || len(models) != 1 || models[0] != "claude-opus-5" {
		t.Fatalf("anthropic shape: %v %v", models, err)
	}
	if meta["claude-opus-5"].Context != 1000000 || meta["claude-opus-5"].MaxOut != 128000 {
		t.Fatalf("the published window must be carried, got %+v", meta["claude-opus-5"])
	}

	chatgpt := `{"models":[{"slug":"gpt-5.6-sol","context_window":400000}]}`
	models, meta, err = parseModelList([]byte(chatgpt))
	if err != nil || len(models) != 1 || models[0] != "gpt-5.6-sol" {
		t.Fatalf("chatgpt shape: %v %v", models, err)
	}
	if meta["gpt-5.6-sol"].Context != 400000 {
		t.Fatalf("context_window must be carried, got %+v", meta["gpt-5.6-sol"])
	}

	// .
	// .
	// .
	omlx := `{"data":[{"id":"qwen3-coder-30b","max_model_len":262144}]}`
	models, meta, err = parseModelList([]byte(omlx))
	if err != nil || len(models) != 1 || models[0] != "qwen3-coder-30b" {
		t.Fatalf("local OpenAI-compatible shape: %v %v", models, err)
	}
	if meta["qwen3-coder-30b"].Context != 262144 {
		t.Fatalf("max_model_len must be carried, got %+v", meta["qwen3-coder-30b"])
	}

	// .
	both := `{"data":[{"id":"m","max_input_tokens":1000,"context_length":2000,"max_model_len":3000},{"id":"n","context_length":2000,"max_model_len":3000}]}`
	if _, meta, err = parseModelList([]byte(both)); err != nil {
		t.Fatalf("mixed shape: %v", err)
	}
	if meta["m"].Context != 1000 || meta["n"].Context != 2000 {
		t.Fatalf("max_model_len outranked a declared input window: %+v %+v", meta["m"], meta["n"])
	}

	local := `{"models":[{"name":"llama3.3"}]}`
	if models, _, err = parseModelList([]byte(local)); err != nil || len(models) != 1 || models[0] != "llama3.3" {
		t.Fatalf("local-runner shape: %v %v", models, err)
	}

	// .
	// .
	// .
	// .
	// .
	// .
	alias := `{"data":[{"id":"deepseek-v4.1-flash","canonical_id":"DeepSeek-V4.1-Ember-oQ4e-mtp","canonical_slug":"DeepSeek-V4.1-Ember-oQ4e-mtp","context_length":262144,"max_model_len":262144}]}`
	models, meta, err = parseModelList([]byte(alias))
	if err != nil || !reflect.DeepEqual(models, []string{"deepseek-v4.1-flash"}) {
		t.Fatalf("the list must stay the server's ids: %v %v", models, err)
	}
	if meta["deepseek-v4.1-flash"].Context != 262144 || meta["DeepSeek-V4.1-Ember-oQ4e-mtp"].Context != 262144 {
		t.Fatalf("the window must be found under the canonical name too, got %+v", meta)
	}

	// .
	// .
	// .
	for _, body := range []string{
		`{"data":[{"id":"role","canonical_id":"X","context_length":100},{"id":"X","context_length":200}]}`,
		`{"data":[{"id":"X","context_length":200},{"id":"role","canonical_id":"X","context_length":100}]}`,
	} {
		if _, meta, err = parseModelList([]byte(body)); err != nil || meta["X"].Context != 200 || meta["role"].Context != 100 {
			t.Fatalf("an alias's claim overrode the model's own row: %+v %v", meta, err)
		}
	}

	if _, _, err = parseModelList([]byte("not json")); err == nil {
		t.Fatal("a non-JSON body must be an error, not an empty list")
	}
}

// .
// .
func TestALiveListSurvivesAFailedReprobe(t *testing.T) {
	dir := t.TempDir()
	entry := providerEntry{Name: "provider", APIType: "openai", URL: "http://127.0.0.1:1/v1", APIKey: "k", Models: []string{"configured"}}
	writeTestProviders(t, dir, entry)
	a := New(&Config{SourcePath: filepath.Join(dir, "config.json")})
	a.provStatus = map[string]providerProbe{entry.Name: {
		state: "ok", models: []string{"live"}, checkedAt: time.Now().Add(-2 * providerStatusTTL), key: probeKey(entry),
	}}
	providers := a.providerDirectoryLive()
	if len(providers) != 1 || providers[0].Status != "unreachable" {
		t.Fatalf("a re-probe against a closed port must fail: %+v", providers)
	}
	if want := []string{"configured", "live"}; !reflect.DeepEqual(providers[0].Models, want) {
		t.Fatalf("the last live list must survive a failed re-probe: %v, want %v", providers[0].Models, want)
	}
}

// .
// .
func TestTheDirectoryShowsTheSeedsNewerModelsForAnExistingFile(t *testing.T) {
	var seed providerRegistry
	if err := json.Unmarshal(embeddedProviders, &seed); err != nil {
		t.Fatal(err)
	}
	var name string
	var seedList []string
	for _, e := range seed.Providers {
		if len(e.Models) > 1 {
			name, seedList = e.Name, e.Models
			break
		}
	}
	if name == "" {
		t.Skip("the seed lists no entry with several models")
	}
	dir := t.TempDir()
	entry := providerEntry{Name: name, APIType: "openai", URL: "https://provider.example/v1", Models: seedList[:1]}
	writeTestProviders(t, dir, entry)
	a := New(&Config{SourcePath: filepath.Join(dir, "config.json")})
	a.provStatus = map[string]providerProbe{name: {state: "unreachable", checkedAt: time.Now(), key: probeKey(entry)}}
	info := a.providerDirectoryLive()[0]
	for _, m := range seedList {
		found := false
		for _, g := range info.Models {
			if g == m {
				found = true
			}
		}
		if !found {
			t.Fatalf("seed model %q missing from %v", m, info.Models)
		}
	}
	if !reflect.DeepEqual(info.ConfiguredModels, seedList[:1]) {
		t.Fatalf("the operator's file must not be rewritten by the seed: %v", info.ConfiguredModels)
	}
}

// .
func TestAnExplicitAskRefreshesTheStatusTheDirectoryShows(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"id":"fresh","context_length":200000}]}`))
	}))
	t.Cleanup(server.Close)
	entry := providerEntry{Name: "provider", URL: server.URL, Models: []string{"configured"}}
	a := New(&Config{})
	if _, err := a.discoverForProvider(context.Background(), &providerRegistry{Providers: []providerEntry{entry}}, entry.Name, ""); err != nil {
		t.Fatal(err)
	}
	a.provMu.Lock()
	st := a.provStatus[entry.Name]
	a.provMu.Unlock()
	if st.state != "ok" || !reflect.DeepEqual(st.models, []string{"fresh"}) || st.meta["fresh"].Context != 200000 {
		t.Fatalf("status after an explicit ask: state %q models %v meta %+v", st.state, st.models, st.meta)
	}
}

// .
// .
func TestTheBootAsksTheProviderForTheWindowItDoesNotDeclare(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"id":"m","context_length":262144,"max_tokens":8192}]}`))
	}))
	t.Cleanup(server.Close)
	dir := t.TempDir()
	writeTestProviders(t, dir, providerEntry{Name: "Vendor-like", APIType: "openai", URL: server.URL, APIKey: "k", DefaultModel: "m", Models: []string{"m"}})
	cfg := &Config{SourcePath: filepath.Join(dir, "config.json")}
	cfg.LLM.Provider = "Vendor-like"
	cfg.LLM.Model = "m"
	a := New(cfg)
	reg, err := a.loadProviders()
	if err != nil {
		t.Fatal(err)
	}
	a.askWindowIfUndeclaredIn(reg, cfg.LLM)
	_, entry, err := a.resolveLLMConfig(cfg.LLM, reg)
	if err != nil {
		t.Fatal(err)
	}
	if entry.ContextLength != 262144 || entry.MaxOutputTokens <= 0 {
		t.Fatalf("the window the provider lists must be the one resolved: context %d, output %d", entry.ContextLength, entry.MaxOutputTokens)
	}
}

// .
// .
// .
// .
func TestTheWindowIsFoundUnderTheNameTheServerCallsCanonical(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"id":"deepseek-v4.1-flash","canonical_id":"DeepSeek-V4.1-Ember-oQ4e-mtp","context_length":262144}]}`))
	}))
	t.Cleanup(server.Close)
	dir := t.TempDir()
	writeTestProviders(t, dir, providerEntry{Name: "Local (oMLX)", APIType: "openai", URL: server.URL + "/v1", APIKey: "k", DefaultModel: "DeepSeek-V4.1-Ember-oQ4e-mtp"})
	cfg := &Config{SourcePath: filepath.Join(dir, "config.json")}
	cfg.LLM.Provider = "Local (oMLX)"
	a := New(cfg)
	reg, err := a.loadProviders()
	if err != nil {
		t.Fatal(err)
	}
	a.askWindowIfUndeclaredIn(reg, cfg.LLM)
	_, entry, err := a.resolveLLMConfig(cfg.LLM, reg)
	if err != nil {
		t.Fatal(err)
	}
	if entry.ContextLength != 262144 {
		t.Fatalf("the window published under the canonical name must be the one resolved: context %d", entry.ContextLength)
	}
	if budget, src := promptBudgetFor(entry, 0); src != budgetDerived || budget != 262144-defaultOutputReserve-promptSafetyTokens {
		t.Fatalf("the budget must derive from that window: %d (%s)", budget, src)
	}
}

// .
// .
// .
func TestAnUnlistedModelIsNamedAsUnlisted(t *testing.T) {
	lines := logsink.CaptureForTest(t)

	dir := t.TempDir()
	entry := providerEntry{Name: "Local", APIType: "openai", URL: "https://provider.example/v1", APIKey: "k", DefaultModel: "typed-by-hand"}
	writeTestProviders(t, dir, entry)
	cfg := &Config{SourcePath: filepath.Join(dir, "config.json")}
	cfg.LLM.Provider = "Local"
	a := New(cfg)
	a.provStatus = map[string]providerProbe{entry.Name: {
		state: "ok", models: []string{"a", "b"}, meta: map[string]modelMeta{"a": {Context: 1000}, "b": {Context: 1000}},
		checkedAt: time.Now(), key: probeKey(entry),
	}}
	if _, resolved, err := a.resolveLLMConfig(cfg.LLM, &providerRegistry{Providers: []providerEntry{entry}}); err != nil || resolved.ContextLength != 0 {
		t.Fatalf("an unlisted name has no window to resolve: %d %v", resolved.ContextLength, err)
	}
	joined := lines.String()
	if !strings.Contains(joined, `lists 2 model(s) and none is named "typed-by-hand"`) {
		t.Fatalf("the miss must name the list and the name, got: %q", joined)
	}

	// .
	lines.Reset()
	a.provStatus = nil
	if _, _, err := a.resolveLLMConfig(cfg.LLM, &providerRegistry{Providers: []providerEntry{entry}}); err != nil {
		t.Fatal(err)
	}
	if joined := lines.String(); strings.Contains(joined, "none is named") {
		t.Fatalf("a provider that listed nothing must not be reported as listing: %q", joined)
	}
}

// .
// .
func TestAModelIsOfferedUnderTheNameTheServerCallsCanonical(t *testing.T) {
	models, meta, err := parseModelList([]byte(`{"data":[{"id":"frontier-text","canonical_id":"GLM-5.2-Alis","context_length":262144}]}`))
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]bool{"frontier-text": true, "GLM-5.2-Alis": true, "typo": false} {
		if got := modelOffered(models, meta, name); got != want {
			t.Fatalf("modelOffered(%q) = %v, want %v", name, got, want)
		}
	}
}

// .
// .
// .
// .
func TestACanonicalNameWithoutLimitsIsStillOffered(t *testing.T) {
	for _, field := range []string{"canonical_id", "canonical_slug"} {
		models, meta, err := parseModelList([]byte(`{"data":[{"id":"alias","` + field + `":"canonical"}]}`))
		if err != nil {
			t.Fatal(err)
		}
		if !modelOffered(models, meta, "alias") || !modelOffered(models, meta, "canonical") {
			t.Fatalf("%s: a published name was refused for lacking limits: models=%v meta=%v", field, models, meta)
		}
		if m := meta["canonical"]; m.Context != 0 || m.MaxOut != 0 {
			t.Fatalf("%s: limits were invented for a row that published none: %+v", field, m)
		}
		if !reflect.DeepEqual(models, []string{"alias"}) {
			t.Fatalf("%s: the list must stay the server's ids: %v", field, models)
		}
	}
	_, meta, err := parseModelList([]byte(`{"data":[{"id":"role","canonical_id":"X"},{"id":"X","context_length":200}]}`))
	if err != nil || meta["X"].Context != 200 {
		t.Fatalf("an alias without limits overrode the model's own row: %+v %v", meta, err)
	}
	if _, _, err := parseModelList([]byte(`{"data":[{"id":"","canonical_id":"nameless"}]}`)); err != nil {
		t.Fatal(err)
	}
}
