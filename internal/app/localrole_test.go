package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/prompt"
	"github.com/aiii-dot-id/aii-os/internal/ring"
)

// The checkbox's semantics, tested at the seams that carry them: the
// config field parses, the provider flag parses, the shared provider
// probe answers and CACHES, and the local-entry pick is
// first-marked-wins.

func TestPreferLocalForRolesParses(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"agency":{"prefer_local_for_roles":true}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Agency.PreferLocalForRoles {
		t.Fatal("the checkbox did not parse")
	}
}

func TestProviderLocalFlagParses(t *testing.T) {
	var e providerEntry
	if err := json.Unmarshal([]byte(`{"name":"Metal","url":"http://10.0.0.2:8080/v1","local":true}`), &e); err != nil {
		t.Fatal(err)
	}
	if !e.Local {
		t.Fatal("local flag did not parse")
	}
	reg := &providerRegistry{Providers: []providerEntry{{Name: "cloud"}, e, {Name: "second", Local: true}}}
	got, ok := localProviderEntry(reg)
	if !ok || got.Name != "Metal" {
		t.Fatalf("first-marked must win, got %q ok=%v", got.Name, ok)
	}
}

func TestLocalRoutingUsesProviderProbeCache(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.URL.Path != "/v1/models" {
			t.Errorf("probe hit %s, want /v1/models", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"local-model"}]}`))
	}))
	defer srv.Close()

	a := &App{}
	entry := providerEntry{Name: "Metal", URL: srv.URL}
	reg := &providerRegistry{Providers: []providerEntry{entry}}
	if got := a.probeProviders(reg)[entry.Name].state; got != "ok" {
		t.Fatalf("live provider state = %q, want ok", got)
	}
	if got := a.probeProviders(reg)[entry.Name].state; got != "ok" || hits.Load() != 1 {
		t.Fatalf("second call inside the TTL must be cached (state=%q hits=%d)", got, hits.Load())
	}

	// Expire the shared cache without a second routing-specific TTL.
	a.provMu.Lock()
	stale := a.provStatus[entry.Name]
	stale.checkedAt = time.Now().Add(-providerStatusTTL - time.Second)
	a.provStatus[entry.Name] = stale
	a.provMu.Unlock()
	srv.Close()
	if got := a.probeProviders(reg)[entry.Name].state; got == "ok" {
		t.Fatal("a dead provider still read alive after cache expiry")
	}
	before := hits.Load()
	_ = a.probeProviders(reg)
	if hits.Load() != before {
		t.Fatal("dead provider was reprobed inside the shared TTL")
	}
}

func TestRunTargetBindsRoutedClientAndBudget(t *testing.T) {
	dir := t.TempDir()
	writeTestProviders(t, dir,
		providerEntry{Name: "active", URL: "http://active.invalid", DefaultModel: "main", Default: true},
		providerEntry{
			Name: "review", URL: "http://review.invalid", DefaultModel: "small",
			ContextLength: 12000, MaxOutputTokens: 1000,
		},
	)
	a := &App{
		cfg: &Config{
			SourcePath: filepath.Join(dir, "config.json"),
			LLM:        LLMConfig{Provider: "active", Model: "main"},
			Prompt:     PromptConfig{MaxTokens: 32000},
			Agency: AgencyConfig{Roles: map[string]RoleRoute{
				"review": {Provider: "review", Model: "small"},
			}},
		},
		composer: prompt.New(ring.NewManager(), 32000),
		llmSwap: newSwappableLLM(llm.New(&llm.ClientConfig{
			Endpoint: "http://active.invalid", Model: "main",
		})),
	}

	routed := a.resolveRunTarget("review")
	if routed.modelID != "small" || routed.fallback {
		t.Fatalf("routed target = model %q fallback=%v", routed.modelID, routed.fallback)
	}
	if want := 12000 - 1000 - promptSafetyTokens; routed.budget != want {
		t.Fatalf("routed budget = %d, want %d", routed.budget, want)
	}

	fallback := a.resolveRunTarget("missing")
	if fallback.modelID != "main" || !fallback.fallback || fallback.budget != 32000 {
		t.Fatalf("fallback target = model %q fallback=%v budget=%d",
			fallback.modelID, fallback.fallback, fallback.budget)
	}
}

func TestActiveRunTargetSnapshotsClientAndBudget(t *testing.T) {
	a := &App{
		composer: prompt.New(ring.NewManager(), 100),
		llmSwap: newSwappableLLM(llm.New(&llm.ClientConfig{
			Endpoint: "http://active.invalid", Model: "a",
		})),
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 200; i++ {
			model, budget := "a", 100
			if i%2 == 1 {
				model, budget = "b", 200
			}
			a.cfgMu.Lock()
			a.composer.SetMaxTokens(budget)
			runtime.Gosched() // widen the old client/budget tear window
			a.llmSwap.Swap(llm.New(&llm.ClientConfig{
				Endpoint: "http://active.invalid", Model: model,
			}))
			a.cfgMu.Unlock()
		}
	}()
	for {
		target := a.activeRunTarget(false)
		if (target.modelID == "a" && target.budget != 100) ||
			(target.modelID == "b" && target.budget != 200) {
			t.Fatalf("torn run target: model=%q budget=%d", target.modelID, target.budget)
		}
		select {
		case <-done:
			return
		default:
		}
	}
}
