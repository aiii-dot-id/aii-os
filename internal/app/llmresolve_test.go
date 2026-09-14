package app

// .
// .
// .
// .
// .
// .

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// .
// .
// .
// .
// .
func withTestProvider(t *testing.T, dir, name, url, model, key string) LLMConfig {
	t.Helper()
	writeTestProviders(t, dir, providerEntry{
		Name: name, APIType: "openai", URL: url, APIKey: key,
		DefaultModel: model, Default: true,
	})
	return LLMConfig{Provider: name, Model: model}
}

// .
// .
func writeTestProviders(t *testing.T, dir string, entries ...providerEntry) {
	t.Helper()
	data, err := json.MarshalIndent(providerRegistry{Providers: entries}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "providers.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// .
// .
func resolverApp(dir string, llmCfg LLMConfig) *App {
	return &App{cfg: &Config{LLM: llmCfg, SourcePath: filepath.Join(dir, "config.json")}}
}

// .
// .
// .
func TestResolveLLMPointer(t *testing.T) {
	dir := t.TempDir()
	writeTestProviders(t, dir,
		providerEntry{Name: "alpha", APIType: "openai", URL: "https://alpha.example/v1", APIKey: "ka", DefaultModel: "am", Default: true},
		providerEntry{Name: "beta", APIType: "anthropic", URL: "https://beta.example", APIKey: "kb", DefaultModel: "bm"},
	)

	// .
	a := resolverApp(dir, LLMConfig{})
	cc, entry, err := a.resolveLLM()
	if err != nil {
		t.Fatal(err)
	}
	if entry.Name != "alpha" || cc.Endpoint != "https://alpha.example/v1" || cc.Model != "am" || cc.APIKey != "ka" {
		t.Fatalf("empty pointer must resolve the default entry: %+v %+v", cc, entry)
	}
	if cc.Provider != "openai" {
		t.Fatalf("api_type must drive the client dialect, got %q", cc.Provider)
	}

	// .
	a = resolverApp(dir, LLMConfig{Provider: "beta", Model: "bm-x"})
	cc, entry, err = a.resolveLLM()
	if err != nil {
		t.Fatal(err)
	}
	if entry.Name != "beta" || cc.Model != "bm-x" {
		t.Fatalf("named pointer must resolve its entry: %+v", cc)
	}
	if cc.Provider != "anthropic" {
		t.Fatalf("api_type \"anthropic\" must reach ClientConfig.Provider, got %q", cc.Provider)
	}

	// .
	a = resolverApp(dir, LLMConfig{Provider: "beta"})
	if cc, _, err = a.resolveLLM(); err != nil || cc.Model != "bm" {
		t.Fatalf("empty model must fall back to default_model: %v %+v", err, cc)
	}

	// .
	a = resolverApp(dir, LLMConfig{Provider: "ghost"})
	if _, _, err = a.resolveLLM(); err == nil {
		t.Fatal("a pointer at no entry must refuse")
	}

	// .
	writeTestProviders(t, dir, providerEntry{Name: "solo", URL: "https://solo.example", DefaultModel: "sm"})
	a = resolverApp(dir, LLMConfig{})
	if _, _, err = a.resolveLLM(); err == nil {
		t.Fatal("no default-flagged entry must refuse an empty pointer")
	}

	// .
	writeTestProviders(t, dir, providerEntry{Name: "bare", URL: "https://bare.example", Default: true})
	a = resolverApp(dir, LLMConfig{})
	if _, _, err = a.resolveLLM(); err == nil {
		t.Fatal("no model anywhere must refuse")
	}
}

// .
// .
func TestResolveLLMKeyPrecedence(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AII_TEST_ENTRY_KEY", "from-entry-env")
	t.Setenv("AII_TEST_GLOBAL_KEY", "from-global-env")

	// .
	writeTestProviders(t, dir, providerEntry{
		Name: "p", URL: "https://p.example", APIKey: "stored",
		APIKeyEnv: "AII_TEST_ENTRY_KEY", DefaultModel: "m", Default: true,
	})
	a := resolverApp(dir, LLMConfig{APIKeyEnv: "AII_TEST_GLOBAL_KEY"})
	if cc, _, err := a.resolveLLM(); err != nil || cc.APIKey != "stored" {
		t.Fatalf("stored key must win: %v %q", err, cc.APIKey)
	}

	// .
	writeTestProviders(t, dir, providerEntry{
		Name: "p", URL: "https://p.example",
		APIKeyEnv: "AII_TEST_ENTRY_KEY", DefaultModel: "m", Default: true,
	})
	if cc, _, err := a.resolveLLM(); err != nil || cc.APIKey != "from-entry-env" {
		t.Fatalf("per-entry env must beat the global env: %v %q", err, cc.APIKey)
	}

	// .
	writeTestProviders(t, dir, providerEntry{
		Name: "p", URL: "https://p.example", DefaultModel: "m", Default: true,
	})
	if cc, _, err := a.resolveLLM(); err != nil || cc.APIKey != "from-global-env" {
		t.Fatalf("global env must be the last fallback: %v %q", err, cc.APIKey)
	}
}

// .
// .
// .
// .
// .
func TestResolveLLMVisibleFloorClamp(t *testing.T) {
	dir := t.TempDir()

	// .
	writeTestProviders(t, dir, providerEntry{
		Name: "p", URL: "https://p.example", APIKey: "k", DefaultModel: "m", Default: true,
		ThinkingBudget: 4096, MaxOutputTokens: 4096,
	})
	a := resolverApp(dir, LLMConfig{})
	cc, entry, err := a.resolveLLM()
	if err != nil {
		t.Fatal(err)
	}
	if cc.ThinkingBudget != 3072 || entry.ThinkingBudget != 3072 {
		t.Fatalf("trap pairing must clamp to 3072, got cc=%d entry=%d", cc.ThinkingBudget, entry.ThinkingBudget)
	}

	// .
	writeTestProviders(t, dir, providerEntry{
		Name: "p", URL: "https://p.example", APIKey: "k", DefaultModel: "m", Default: true,
		ThinkingBudget: 8192,
	})
	if cc, _, err = a.resolveLLM(); err != nil || cc.ThinkingBudget != 7168 {
		t.Fatalf("default output must retain the visible floor: %v %d", err, cc.ThinkingBudget)
	}

	// .
	writeTestProviders(t, dir, providerEntry{
		Name: "p", URL: "https://p.example", APIKey: "k", DefaultModel: "m", Default: true,
		ThinkingBudget: 400, MaxOutputTokens: 512,
	})
	if cc, _, err = a.resolveLLM(); err != nil || cc.ThinkingBudget != 0 {
		t.Fatalf("sub-floor output must zero the thinking budget: %v %d", err, cc.ThinkingBudget)
	}

	// .
	writeTestProviders(t, dir, providerEntry{
		Name: "p", URL: "https://p.example", APIKey: "k", DefaultModel: "m", Default: true,
		ThinkingBudget: 2048, MaxOutputTokens: 4096,
	})
	if cc, _, err = a.resolveLLM(); err != nil || cc.ThinkingBudget != 2048 {
		t.Fatalf("healthy pairing must pass: %v %d", err, cc.ThinkingBudget)
	}
}

func TestResolveLLMModelWindow(t *testing.T) {
	dir := t.TempDir()
	writeTestProviders(t, dir, providerEntry{
		Name: "p", URL: "https://p.example", DefaultModel: "m", Default: true,
		ContextLength: 16384, MaxOutputTokens: 4096,
	})
	a := resolverApp(dir, LLMConfig{})
	cc, entry, err := a.resolveLLM()
	if err != nil {
		t.Fatal(err)
	}
	if cc.MaxOutputTokens != 4096 || promptBudgetFor(entry, 32000) != 10240 {
		t.Fatalf("resolved limits did not reach the client: %+v %+v", cc, entry)
	}

	writeTestProviders(t, dir, providerEntry{
		Name: "p", URL: "https://p.example", DefaultModel: "m", Default: true,
		ContextLength: 4096, MaxOutputTokens: 4096,
	})
	if _, _, err := a.resolveLLM(); err == nil {
		t.Fatal("model with no viable input window was accepted")
	}
}

// .
// .
// .
// .
// .
func TestResolveLLMSamplingZeroVsAbsent(t *testing.T) {
	dir := t.TempDir()
	raw := `{"providers":[
		{"name":"zero","url":"https://z.example","api_key":"k","default_model":"m","default":true,
		 "temperature":0,"top_p":0.9,"extra":{"repetition_penalty":1.05}},
		{"name":"absent","url":"https://a.example","api_key":"k","default_model":"m"}
	]}`
	if err := os.WriteFile(filepath.Join(dir, "providers.json"), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}

	a := resolverApp(dir, LLMConfig{Provider: "zero"})
	cc, _, err := a.resolveLLM()
	if err != nil {
		t.Fatal(err)
	}
	if cc.Temperature == nil || *cc.Temperature != 0 {
		t.Fatalf("\"temperature\": 0 must survive as a set zero, got %v", cc.Temperature)
	}
	if cc.TopP == nil || *cc.TopP != 0.9 {
		t.Fatalf("top_p must carry, got %v", cc.TopP)
	}
	if cc.Extra == nil || cc.Extra["repetition_penalty"] != 1.05 {
		t.Fatalf("extra must ride to the client config, got %v", cc.Extra)
	}

	a = resolverApp(dir, LLMConfig{Provider: "absent"})
	if cc, _, err = a.resolveLLM(); err != nil || cc.Temperature != nil || cc.TopP != nil {
		t.Fatalf("absent sampling params must stay nil (omit on the wire): %v %v %v", err, cc.Temperature, cc.TopP)
	}
}
