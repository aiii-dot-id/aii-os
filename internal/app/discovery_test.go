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
	a.providers.runtime.provStatus = map[string]providerProbe{entry.Name: {
		state: "ok", models: []string{"live-b", "live-a"},
		checkedAt: time.Now(), key: probeKey(entry),
	}}

	providers := a.providerDirectory().Providers
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
	models, err := a.discoverForProvider(context.Background(), &providerRegistry{Providers: []providerEntry{entry}}, entry.Name, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"configured", "shared", "live"}; !reflect.DeepEqual(models, want) {
		t.Fatalf("models = %v, want %v", models, want)
	}
}

// Where a model list lives is a property of the URL, not of a dialect
// label. This is the rule that makes an Anthropic base work whether or
// not the operator typed the /v1.
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
		{"https://example.com/version/api", false}, // "version" is not a version
	} {
		if got := apiVersionInPath(tc.url); got != tc.want {
			t.Errorf("%s -> %v, want %v", tc.url, got, tc.want)
		}
	}
}

// Three model-list shapes exist in the wild, and the window each provider
// publishes is the truth we should budget prompts against.
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

	// An OpenAI-compatible local server (oMLX, vLLM) publishes its window
	// as max_model_len and nothing else; without it the runtime budgets
	// prompts against a fallback while the server was telling it the size.
	omlx := `{"data":[{"id":"qwen3-coder-30b","max_model_len":262144}]}`
	models, meta, err = parseModelList([]byte(omlx))
	if err != nil || len(models) != 1 || models[0] != "qwen3-coder-30b" {
		t.Fatalf("local OpenAI-compatible shape: %v %v", models, err)
	}
	if meta["qwen3-coder-30b"].Context != 262144 {
		t.Fatalf("max_model_len must be carried, got %+v", meta["qwen3-coder-30b"])
	}

	// Precedence: the named input limit and context_length still win over it.
	both := `{"data":[{"id":"m","max_input_tokens":1000,"context_length":2000,"max_model_len":3000},{"id":"n","context_length":2000,"max_model_len":3000}]}`
	if _, meta, err = parseModelList([]byte(both)); err != nil {
		t.Fatalf("mixed shape: %v", err)
	}
	if meta["m"].Context != 1000 || meta["n"].Context != 2000 {
		t.Fatalf("max_model_len outranked a declared input window: %+v %+v", meta["m"], meta["n"])
	}

	// A data row that names its window context_window, as the models
	// shape does: read, and outranked by every field read before it.
	windowed := `{"data":[{"id":"w","context_window":131072},{"id":"x","max_model_len":3000,"context_window":4000}]}`
	if _, meta, err = parseModelList([]byte(windowed)); err != nil {
		t.Fatalf("context_window on a data row: %v", err)
	}
	if meta["w"].Context != 131072 || meta["x"].Context != 3000 {
		t.Fatalf("a data row's context_window: %+v %+v, want it carried, and after max_model_len", meta["w"], meta["x"])
	}

	local := `{"models":[{"name":"llama3.3"}]}`
	if models, _, err = parseModelList([]byte(local)); err != nil || len(models) != 1 || models[0] != "llama3.3" {
		t.Fatalf("local-runner shape: %v %v", models, err)
	}

	alias := `{"data":[{"id":"example-model-30b","canonical_id":"Example-Model-30B-Q4-mtp","canonical_slug":"Example-Model-30B-Q4-mtp","context_length":262144,"max_model_len":262144}]}`
	models, meta, err = parseModelList([]byte(alias))
	if err != nil || !reflect.DeepEqual(models, []string{"example-model-30b"}) {
		t.Fatalf("the list must stay the server's ids: %v %v", models, err)
	}
	if meta["example-model-30b"].Context != 262144 || meta["Example-Model-30B-Q4-mtp"].Context != 262144 {
		t.Fatalf("the window must be found under the canonical name too, got %+v", meta)
	}

	// A row listed under its own id is the server's direct word and
	// outranks another row's canonical claim on that name, whatever the
	// order the server lists them in.
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

func TestParseModelListNativeImageCapabilities(t *testing.T) {
	for _, tc := range []struct {
		name  string
		row   string
		known bool
		want  bool
	}{
		{"openrouter image", `"architecture":{"input_modalities":["text","image"]}`, true, true},
		{"openrouter text", `"architecture":{"input_modalities":["text"]}`, true, false},
		{"openrouter output only", `"architecture":{"output_modalities":["image"]}`, false, false},
		{"openrouter empty", `"architecture":{"input_modalities":[]}`, false, false},
		{"openrouter null", `"architecture":{"input_modalities":null}`, false, false},
		{"anthropic image", `"capabilities":{"image_input":{"supported":true}}`, true, true},
		{"anthropic text", `"capabilities":{"image_input":{"supported":false}}`, true, false},
		{"anthropic incomplete", `"capabilities":{"image_input":{}}`, false, false},
		{"anthropic null", `"capabilities":{"image_input":{"supported":null}}`, false, false},
		{"mistral image", `"capabilities":{"vision":true}`, true, true},
		{"mistral text", `"capabilities":{"vision":false}`, true, false},
		{"mistral incomplete", `"capabilities":{"completion_chat":true}`, false, false},
		{"mistral null", `"capabilities":{"vision":null}`, false, false},
		{"no capability", `"context_length":1234,"max_tokens":100`, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			models, meta, err := parseModelList([]byte(`{"data":[{"id":"vision-model",` + tc.row + `}]}`))
			if err != nil || !reflect.DeepEqual(models, []string{"vision-model"}) {
				t.Fatalf("published model: %v %v", models, err)
			}
			got := meta["vision-model"]
			if (got.Images != nil) != tc.known || (got.Images != nil && *got.Images != tc.want) {
				t.Fatalf("image support = %v, want known=%v images=%v", got.Images, tc.known, tc.want)
			}
			if tc.known {
				if _, ok := meta["vision-model"]; !ok || got.Context != 0 || got.MaxOut != 0 {
					t.Fatalf("capability without a window was lost or invented limits: %+v", meta)
				}
			} else if tc.name == "no capability" && (got.Context != 1234 || got.MaxOut != 100) {
				t.Fatalf("unknown capability discarded existing limits: %+v", got)
			}
		})
	}
	_, meta, err := parseModelList([]byte(`{"models":[{"name":"models/vision-model","supportedGenerationMethods":["generateContent"],"inputTokenLimit":1000}]}`))
	if err != nil || meta["vision-model"].Images != nil {
		t.Fatalf("a name or supported method was mistaken for image support: %+v %v", meta, err)
	}
}

func TestNativeImageCapabilitiesFollowCanonicalNames(t *testing.T) {
	for _, field := range []string{"canonical_id", "canonical_slug"} {
		for _, supportsImages := range []bool{false, true} {
			body, err := json.Marshal(map[string]any{"data": []map[string]any{{
				"id": "alias", field: "canonical", "context_length": 2000, "max_tokens": 200,
				"capabilities": map[string]any{"vision": supportsImages},
			}}})
			if err != nil {
				t.Fatal(err)
			}
			models, meta, err := parseModelList(body)
			if err != nil || !reflect.DeepEqual(models, []string{"alias"}) {
				t.Fatalf("%s: provider ids changed: %v %v", field, models, err)
			}
			for _, name := range []string{"alias", "canonical"} {
				got := meta[name]
				if got.Images == nil || *got.Images != supportsImages || got.Context != 2000 || got.MaxOut != 200 {
					t.Fatalf("%s: metadata not carried under %q: %+v", field, name, got)
				}
			}
		}
	}
	for _, tc := range []struct {
		body  string
		known bool
	}{
		{`{"data":[{"id":"role","canonical_id":"X","capabilities":{"vision":true}},{"id":"X","capabilities":{"vision":false}}]}`, true},
		{`{"data":[{"id":"X","capabilities":{"vision":false}},{"id":"role","canonical_id":"X","capabilities":{"vision":true}}]}`, true},
		{`{"data":[{"id":"role","canonical_id":"X","capabilities":{"vision":true}},{"id":"X"}]}`, false},
		{`{"data":[{"id":"X"},{"id":"role","canonical_id":"X","capabilities":{"vision":true}}]}`, false},
	} {
		_, meta, err := parseModelList([]byte(tc.body))
		if err != nil {
			t.Fatal(err)
		}
		if got := meta["role"].Images; got == nil || !*got {
			t.Fatalf("alias lost its own image support: %+v", meta)
		}
		if got := meta["X"].Images; (got != nil) != tc.known || (got != nil && *got) {
			t.Fatalf("alias overrode its canonical model's own row: %+v", meta)
		}
	}
}

func TestALiveListSurvivesAFailedReprobe(t *testing.T) {
	dir := t.TempDir()
	entry := providerEntry{Name: "provider", APIType: "openai", URL: "http://127.0.0.1:1/v1", APIKey: "k", Models: []string{"configured"}}
	writeTestProviders(t, dir, entry)
	a := New(&Config{SourcePath: filepath.Join(dir, "config.json")})
	a.providers.runtime.provStatus = map[string]providerProbe{entry.Name: {
		state: "ok", models: []string{"live"}, checkedAt: time.Now().Add(-2 * providerStatusTTL), key: probeKey(entry),
	}}
	providers := a.providerDirectory().Providers
	if len(providers) != 1 || providers[0].Status != "unreachable" {
		t.Fatalf("a re-probe against a closed port must fail: %+v", providers)
	}
	if want := []string{"configured", "live"}; !reflect.DeepEqual(providers[0].Models, want) {
		t.Fatalf("the last live list must survive a failed re-probe: %v, want %v", providers[0].Models, want)
	}
}

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
	a.providers.runtime.provStatus = map[string]providerProbe{name: {state: "unreachable", checkedAt: time.Now(), key: probeKey(entry)}}
	info := a.providerDirectory().Providers[0]
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

func TestAnExplicitAskRefreshesTheStatusTheDirectoryShows(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"id":"fresh","context_length":200000}]}`))
	}))
	t.Cleanup(server.Close)
	entry := providerEntry{Name: "provider", URL: server.URL, Models: []string{"configured"}}
	a := New(&Config{})
	if _, err := a.discoverForProvider(context.Background(), &providerRegistry{Providers: []providerEntry{entry}}, entry.Name, "", ""); err != nil {
		t.Fatal(err)
	}
	a.providers.runtime.mu.Lock()
	st := a.providers.runtime.provStatus[entry.Name]
	a.providers.runtime.mu.Unlock()
	if st.state != "ok" || !reflect.DeepEqual(st.models, []string{"fresh"}) || st.meta["fresh"].Context != 200000 {
		t.Fatalf("status after an explicit ask: state %q models %v meta %+v", st.state, st.models, st.meta)
	}
}

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
	_, entry, err := a.resolveLLMConfig(Config{LLM: cfg.LLM}, reg)
	if err != nil {
		t.Fatal(err)
	}
	if entry.ContextLength != 262144 || entry.MaxOutputTokens <= 0 {
		t.Fatalf("the window the provider lists must be the one resolved: context %d, output %d", entry.ContextLength, entry.MaxOutputTokens)
	}
}

func TestTheWindowIsFoundUnderTheNameTheServerCallsCanonical(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"id":"example-model-30b","canonical_id":"Example-Model-30B-Q4-mtp","context_length":262144}]}`))
	}))
	t.Cleanup(server.Close)
	dir := t.TempDir()
	writeTestProviders(t, dir, providerEntry{Name: "Local (oMLX)", APIType: "openai", URL: server.URL + "/v1", APIKey: "k", DefaultModel: "Example-Model-30B-Q4-mtp"})
	cfg := &Config{SourcePath: filepath.Join(dir, "config.json")}
	cfg.LLM.Provider = "Local (oMLX)"
	a := New(cfg)
	reg, err := a.loadProviders()
	if err != nil {
		t.Fatal(err)
	}
	a.askWindowIfUndeclaredIn(reg, cfg.LLM)
	_, entry, err := a.resolveLLMConfig(Config{LLM: cfg.LLM}, reg)
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

// A provider that answered with a list that does not carry the name in
// use is said so in the log, with the count — "none was discovered" alone
// read as though the probe had failed, when 25 models had been listed.
func TestAnUnlistedModelIsNamedAsUnlisted(t *testing.T) {
	lines := logsink.CaptureForTest(t)

	dir := t.TempDir()
	entry := providerEntry{Name: "Local", APIType: "openai", URL: "https://provider.example/v1", APIKey: "k", DefaultModel: "typed-by-hand"}
	writeTestProviders(t, dir, entry)
	cfg := &Config{SourcePath: filepath.Join(dir, "config.json")}
	cfg.LLM.Provider = "Local"
	a := New(cfg)
	a.providers.runtime.provStatus = map[string]providerProbe{entry.Name: {
		state: "ok", models: []string{"a", "b"}, meta: map[string]modelMeta{"a": {Context: 1000}, "b": {Context: 1000}},
		checkedAt: time.Now(), key: probeKey(entry),
	}}
	if _, resolved, err := a.resolveLLMConfig(Config{LLM: cfg.LLM}, &providerRegistry{Providers: []providerEntry{entry}}); err != nil || resolved.ContextLength != 0 {
		t.Fatalf("an unlisted name has no window to resolve: %d %v", resolved.ContextLength, err)
	}
	joined := lines.String()
	if !strings.Contains(joined, `lists 2 model(s) and none is named "typed-by-hand"`) {
		t.Fatalf("the miss must name the list and the name, got: %q", joined)
	}

	// Silent provider: nothing listed, nothing to say about the list.
	lines.Reset()
	a.providers.runtime.provStatus = nil
	if _, _, err := a.resolveLLMConfig(Config{LLM: cfg.LLM}, &providerRegistry{Providers: []providerEntry{entry}}); err != nil {
		t.Fatal(err)
	}
	if joined := lines.String(); strings.Contains(joined, "none is named") {
		t.Fatalf("a provider that listed nothing must not be reported as listing: %q", joined)
	}
}

func TestAListedModelWithoutAWindowIsNotCalledUnlisted(t *testing.T) {
	lines := logsink.CaptureForTest(t)
	dir := t.TempDir()
	entry := providerEntry{Name: "Local server", APIType: "openai", URL: "http://127.0.0.1:1/v1", Local: true, DefaultModel: "local-model"}
	writeTestProviders(t, dir, entry)
	cfg := &Config{SourcePath: filepath.Join(dir, "config.json")}
	cfg.LLM.Provider = "Local server"
	a := New(cfg)
	resolve := func(model string, listed ...string) string {
		t.Helper()
		lines.Reset()
		a.providers.runtime.provStatus = map[string]providerProbe{entry.Name: {state: "ok", models: listed, meta: map[string]modelMeta{}, checkedAt: time.Now(), key: probeKey(entry)}}
		llmCfg := cfg.LLM
		llmCfg.Model = model
		if _, resolved, err := a.resolveLLMConfig(Config{LLM: llmCfg}, &providerRegistry{Providers: []providerEntry{entry}}); err != nil || resolved.ContextLength != 0 {
			t.Fatalf("no window was published, none may be resolved: %d %v", resolved.ContextLength, err)
		}
		return lines.String()
	}
	if got := resolve("local-model", "local-model", "other"); !strings.Contains(got, `provider "Local server" lists "local-model" but publishes no context window for it`) || strings.Contains(got, "none is named") {
		t.Fatalf("a listed model without a window was not said as listed: %q", got)
	}
	if got := resolve("typed-by-hand", "local-model", "other"); !strings.Contains(got, `lists 2 model(s) and none is named "typed-by-hand"`) || strings.Contains(got, "publishes no context window") {
		t.Fatalf("an unlisted model was not said as unlisted: %q", got)
	}
}

// A WINDOW THE MODEL'S OWN ROW GIVES IS NOT ONE THE OPERATOR IS ASKED FOR.
// Both lines above were said before the row was asked, on every resolve
// (each Settings render, each routed spawn, each boot), for an entry that
// declares no window and a list that carries none: they told the operator
// to set a number the row then supplied.
func TestAWindowTheModelsRowGivesIsNotAskedOfTheOperator(t *testing.T) {
	lines := logsink.CaptureForTest(t)
	dir := t.TempDir()
	entry := providerEntry{Name: "Hosted", APIType: "openai", URL: "https://provider.example/v1", APIKey: "k", DefaultModel: "rowed"}
	writeTestProviders(t, dir, entry)
	cfg := &Config{SourcePath: filepath.Join(dir, "config.json")}
	cfg.LLM.Provider = "Hosted"
	a := New(cfg)
	reg := &providerRegistry{Providers: []providerEntry{entry},
		ModelCapabilities: map[string]modelCapability{"rowed": {ContextLength: 64000}}}
	resolve := func(model string, listed ...string) (int, string) {
		t.Helper()
		lines.Reset()
		a.providers.runtime.provStatus = map[string]providerProbe{entry.Name: {state: "ok", models: listed, meta: map[string]modelMeta{}, checkedAt: time.Now(), key: probeKey(entry)}}
		llmCfg := cfg.LLM
		llmCfg.Model = model
		_, resolved, err := a.resolveLLMConfig(Config{LLM: llmCfg}, reg)
		if err != nil {
			t.Fatal(err)
		}
		return resolved.ContextLength, lines.String()
	}
	for name, listed := range map[string][]string{"listed with no window": {"rowed", "other"}, "not in the list": {"other"}} {
		window, said := resolve("rowed", listed...)
		if window != 64000 {
			t.Fatalf("%s: resolved at %d, want the row's 64000", name, window)
		}
		if strings.Contains(said, "set context_length on the entry") {
			t.Fatalf("%s: the row gives the window and the operator was told to set one: %q", name, said)
		}
	}
	// A model with no row is still said, as before.
	if window, said := resolve("rowless", "rowless"); window != 0 || !strings.Contains(said, `lists "rowless" but publishes no context window for it`) {
		t.Fatalf("a model no row gives a window is no longer said: %d %q", window, said)
	}
}

// A birth that names the model the way the server names it canonically
// names something the server offers.
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

// A NAME THE SERVER PUBLISHES IS OFFERED WHETHER OR NOT THE ROW CARRIES
// LIMITS: membership is not a budget. The canonical name of a row with
// no window is kept with zero limits — unknown, never invented — and a
// row's own id still outranks an alias's claim on the same name.
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

// A MODEL'S OWN WINDOW HOLDS ITS ENTRY'S. An entry declares one window for
// every model it lists, so a smaller model on it was budgeted at the
// largest model's number: the fit and the admission passed, the provider
// refused the turn and every later one, and nothing folded. A capability
// row's context_length is the model's own; the resolved window and the
// prompt budget derived from it are held to it, the operator's entry is not
// changed, and a larger model on the same entry keeps the entry's number.
// The numbers here are the test's, not any vendor's.
func TestAModelsOwnWindowHoldsTheEntrysWindow(t *testing.T) {
	dir := t.TempDir()
	entry := providerEntry{Name: "Shared", APIType: "openai", URL: "https://provider.example/v1", APIKey: "k",
		DefaultModel: "large", ContextLength: 256000, MaxOutputTokens: 8000}
	writeTestProviders(t, dir, entry)
	cfg := &Config{SourcePath: filepath.Join(dir, "config.json")}
	cfg.LLM.Provider = "Shared"
	a := New(cfg)
	reg := &providerRegistry{Providers: []providerEntry{entry},
		ModelCapabilities: map[string]modelCapability{"small": {ContextLength: 64000}}}

	resolve := func(model string) providerEntry {
		t.Helper()
		llmCfg := cfg.LLM
		llmCfg.Model = model
		_, resolved, err := a.resolveLLMConfig(Config{LLM: llmCfg}, reg)
		if err != nil {
			t.Fatal(err)
		}
		return resolved
	}
	small := resolve("small")
	if small.ContextLength != 64000 {
		t.Fatalf("the small model resolved at %d, want its own window 64000", small.ContextLength)
	}
	if budget, _ := promptBudgetFor(small, 0); budget != 64000-8000-promptSafetyTokens {
		t.Fatalf("the small model's prompt budget is %d, want it derived from its own window", budget)
	}
	if large := resolve("large"); large.ContextLength != 256000 {
		t.Fatalf("a model with no row of its own resolved at %d, want the entry's 256000", large.ContextLength)
	}
	if reg.Providers[0].ContextLength != 256000 {
		t.Fatal("the operator's entry was changed")
	}

	eff := reg.effective()
	// The row never widens what the entry declares, and gives a window
	// where the entry declares none.
	narrow := providerEntry{Name: "Narrow", ContextLength: 32000}
	if got := limitModelWindow(narrow, "small", eff); got.ContextLength != 32000 {
		t.Fatalf("the row widened the operator's smaller window to %d", got.ContextLength)
	}
	if got := limitModelWindow(providerEntry{Name: "Undeclared"}, "small", eff); got.ContextLength != 64000 {
		t.Fatalf("an entry with no window resolved at %d, want the model's own", got.ContextLength)
	}

	// A row that states a negative window is refused, not passed over.
	bad := &providerRegistry{Providers: []providerEntry{entry},
		ModelCapabilities: map[string]modelCapability{"small": {ContextLength: -1}}}
	llmCfg := cfg.LLM
	llmCfg.Model = "small"
	if _, _, err := a.resolveLLMConfig(Config{LLM: llmCfg}, bad); err == nil || !strings.Contains(err.Error(), "negative context window") {
		t.Fatalf("a negative row window resolved: %v", err)
	}
}
