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
// probe answers and CACHES, and the local-entry pick is the first marked
// entry that answers.

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
	if got := localEntries(reg); len(got) != 2 || got[0].Name != "Metal" || got[1].Name != "second" {
		t.Fatalf("the marked entries, in file order: %+v", got)
	}
}

// A DEAD LOCAL ENTRY DOES NOT SHADOW A LIVE ONE. The seed ships "Local
// server" at a default address; an operator who adds their own local entry
// after it, and ticks the checkbox, is routed to the one that answers. Of
// two that answer, the first serves.
func TestADeadLocalEntryDoesNotShadowALiveOne(t *testing.T) {
	live := func() string {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"data":[{"id":"local-model"}]}`))
		}))
		t.Cleanup(srv.Close)
		return srv.URL + "/v1"
	}
	route := func(entries ...providerEntry) runTarget {
		dir := t.TempDir()
		writeTestProviders(t, dir, append([]providerEntry{{Name: "active", URL: "http://active.invalid", DefaultModel: "main", Default: true}}, entries...)...)
		a := &App{
			cfg: &Config{
				SourcePath: filepath.Join(dir, "config.json"),
				LLM:        LLMConfig{Provider: "active", Model: "main"},
				Prompt:     PromptConfig{MaxTokens: 32000},
				Agency:     AgencyConfig{PreferLocalForRoles: true},
			},
			composer: prompt.New(ring.NewManager(), 32000),
			llmSwap:  newSwappableLLM(llm.New(&llm.ClientConfig{Endpoint: "http://active.invalid", Model: "main"})),
		}
		return a.resolveRunTarget("review")
	}
	seed := seedLocalEntry(t)
	seed.URL = "http://127.0.0.1:1/v1" // left where nothing answers
	seed.DefaultModel = "seed-model"
	mine := providerEntry{Name: "Metal", APIType: "openai", URL: live(), Local: true, DefaultModel: "metal-model"}

	if got := route(seed, mine); got.modelID != "metal-model" || got.fellBack() {
		t.Fatalf("a dead seed entry shadowed the live local one: model %q fallback=%v", got.modelID, got.fellBack())
	}
	seed.URL = live()
	if got := route(seed, mine); got.modelID != "seed-model" || got.fellBack() {
		t.Fatalf("of two live local entries the first did not serve: model %q fallback=%v", got.modelID, got.fellBack())
	}
	mine.URL = "http://127.0.0.1:1/v1"
	seed.URL = "http://127.0.0.1:1/v1"
	if got := route(seed, mine); got.modelID != "main" || !got.fellBack() {
		t.Fatalf("with no local entry answering the active model did not serve: model %q fallback=%v", got.modelID, got.fellBack())
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
	a.providers.runtime.mu.Lock()
	stale := a.providers.runtime.provStatus[entry.Name]
	stale.checkedAt = time.Now().Add(-providerStatusTTL - time.Second)
	a.providers.runtime.provStatus[entry.Name] = stale
	a.providers.runtime.mu.Unlock()
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
	if routed.modelID != "small" || routed.fellBack() {
		t.Fatalf("routed target = model %q fallback=%v", routed.modelID, routed.fellBack())
	}
	if want := 12000 - 1000 - promptSafetyTokens; routed.budget != want {
		t.Fatalf("routed budget = %d, want %d", routed.budget, want)
	}

	fallback := a.resolveRunTarget("missing")
	if fallback.modelID != "main" || !fallback.fellBack() || fallback.budget != 32000 {
		t.Fatalf("fallback target = model %q fallback=%v budget=%d",
			fallback.modelID, fallback.fellBack(), fallback.budget)
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
		target := a.activeRunTarget("")
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
