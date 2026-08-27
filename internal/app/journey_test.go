package app

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
)

// THE OPERATOR'S JOURNEY, not a function. "I picked zAI and GLM-5.3 and
// it did not work" — this walks that exact sequence through the real
// appliers the websocket verbs call, so the failure mode is reproduced
// and the guidance is asserted rather than assumed.
func TestOperatorPicksAProviderAndAModel(t *testing.T) {
	// The use-door does more than list models: it requires a real answer
	// to a minimal inference request before it will commit a switch. So
	// the stand-in has to actually serve, not just enumerate.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// An empty key still produces "Authorization: Bearer " — a
		// NON-empty header carrying no token. A real provider 401s on
		// that, so the stand-in must too, or the test proves nothing.
		if strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer")) == "" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/chat/completions") {
			w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`))
			return
		}
		w.Write([]byte(`{"data":[{"id":"glm-5.3"},{"id":"glm-5.2"}]}`))
	}))
	defer srv.Close()

	dir := t.TempDir()
	writeTestProviders(t, dir,
		providerEntry{Name: "Lilac", APIType: "openai", URL: srv.URL, APIKey: "k-lilac",
			DefaultModel: "glm-5.2", Models: []string{"glm-5.2"}, Default: true},
		providerEntry{Name: "zAI", APIType: "openai", URL: srv.URL,
			DefaultModel: "glm-5.3", Models: []string{"glm-5.3", "glm-5.2"}},
	)
	a := New(&Config{
		LLM:        LLMConfig{Provider: "Lilac", Model: "glm-5.2"},
		SourcePath: filepath.Join(dir, "config.json"),
	})

	// STEP 1 — pick zAI before giving it a key. It must refuse, and the
	// refusal must say what to do; "did not validate" alone is why this
	// felt broken.
	_, err := a.applyConfigChange(map[string]interface{}{
		"llm.provider": "zAI", "llm.model": "glm-5.3",
	})
	if err == nil {
		t.Fatal("switching to a provider with no credential must refuse, not silently half-apply")
	}
	if !strings.Contains(err.Error(), "zAI") {
		t.Errorf("the refusal must name the provider that failed: %v", err)
	}
	t.Logf("step 1 refused as it should: %v", err)

	// The refusal must not have half-applied: the pointer still resolves
	// to the working provider.
	if cc, _, rerr := a.resolveLLM(); rerr != nil || cc.Model != "glm-5.2" {
		t.Fatalf("a refused switch must leave the previous substrate intact, got %v %v", cc.Model, rerr)
	}

	// STEP 2 — give zAI a key, the way the Providers editor does.
	if err := a.setProviderInfo(dashboard.ProviderInfo{
		Name: "zAI", APIType: "openai", Endpoint: srv.URL, APIKey: "k-zai",
		DefaultModel: "glm-5.3", ConfiguredModels: []string{"glm-5.3", "glm-5.2"},
	}); err != nil {
		t.Fatalf("storing a key must succeed: %v", err)
	}
	// And the UI must be able to SEE that it landed — the operator has no
	// other way to tell.
	var stored *dashboard.ProviderInfo
	for _, p := range a.providerDirectoryLive() {
		if p.Name == "zAI" {
			pp := p
			stored = &pp
		}
	}
	if stored == nil || !stored.HasKey {
		t.Fatal("after storing a key the directory must report has_key — otherwise the operator cannot tell it worked")
	}

	// STEP 3 — now the same switch must be accepted.
	if _, err := a.applyConfigChange(map[string]interface{}{
		"llm.provider": "zAI", "llm.model": "glm-5.3",
	}); err != nil {
		t.Fatalf("with a key stored the switch must be accepted: %v", err)
	}
	cc, entry, err := a.resolveLLM()
	if err != nil {
		t.Fatal(err)
	}
	if entry.Name != "zAI" || cc.Model != "glm-5.3" || cc.APIKey != "k-zai" {
		t.Fatalf("resolved to %q/%q key=%q — want zAI/glm-5.3 with its own key", entry.Name, cc.Model, cc.APIKey)
	}
	t.Log("step 3 accepted: zAI / glm-5.3 active with its own key")
}
