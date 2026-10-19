package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/firewall"
	"github.com/aiii-dot-id/aii-os/internal/llm"
)

func TestBothBootsRecordTheProviderTheyResolved(t *testing.T) {
	for _, boot := range []struct {
		name string
		run  func(a *App) error
	}{
		{"live", startLiveForTest},
		{"safe", func(a *App) error { return a.startSafeBoot(safeRecord, "test: SAFE keeps the conversation") }},
	} {
		t.Run(boot.name, func(t *testing.T) {
			dir := t.TempDir()
			keyPath, ledgerPath, dbPath := birthFixture(t, dir, "Provider")
			a := New(safebootConfig(t, dir, "Provider", keyPath, ledgerPath, dbPath))
			t.Cleanup(func() { stopApp(t, a) })
			if err := boot.run(a); err != nil {
				t.Fatalf("%s boot: %v", boot.name, err)
			}
			_, want, err := a.resolveLLM()
			if err != nil {
				t.Fatal(err)
			}
			if got := a.currentProvider(); got.Name == "" || got.Name != want.Name || got.EmbeddingsModel != want.EmbeddingsModel {
				t.Fatalf("after the %s boot the identity is on provider %q (embeddings %q); it resolved %q (embeddings %q)", boot.name, got.Name, got.EmbeddingsModel, want.Name, want.EmbeddingsModel)
			}
			// And the client built from it is recorded with it: what embeds a
			// text reads the two as one (adoptRuntime).
			if entry, client := a.activeRuntime(); client == nil || client != a.llmSwap.Current() || entry.Name != want.Name {
				t.Fatalf("after the %s boot provider %q is recorded with a client: %v; the same client as the swap holds: %v", boot.name, entry.Name, client != nil, client == a.llmSwap.Current())
			}
		})
	}
}

func TestBootDiscoversNativeCapabilitiesWithDeclaredLimitsButNotInSafe(t *testing.T) {
	before := selectedModelDiscovery
	selectedModelDiscovery = (*App).discoverMetaForEntry
	t.Cleanup(func() { selectedModelDiscovery = before })
	for _, boot := range []struct {
		name string
		safe bool
		run  func(*App) error
	}{
		{"live", false, startLiveForTest},
		{"safe", true, func(a *App) error { return a.startSafeBoot(safeRecord, "test: capability discovery stays off in SAFE") }},
	} {
		t.Run(boot.name, func(t *testing.T) {
			type request struct{ path, authorization string }
			requested := make(chan request, 1)
			var calls atomic.Int64
			var discoveries atomic.Int64
			selectedModelDiscovery = func(a *App, ctx context.Context, entry providerEntry, key string) ([]string, map[string]modelMeta, error) {
				discoveries.Add(1)
				return a.discoverMetaForEntry(ctx, entry, key)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				select {
				case requested <- request{r.URL.Path, r.Header.Get("Authorization")}:
				default:
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"data":[{"id":"fixture-model","capabilities":{"vision":true}}]}`))
			}))
			t.Cleanup(server.Close)
			dir := t.TempDir()
			keyPath, ledgerPath, dbPath := birthFixture(t, dir, "Capabilities")
			cfg := safebootConfig(t, dir, "Capabilities", keyPath, ledgerPath, dbPath)
			entry := providerEntry{
				Name: "fixture-provider", APIType: "openai", URL: server.URL + "/v1", Local: true,
				APIKey: "synthetic-key", DefaultModel: "fixture-model", Default: true,
				ContextLength: 65536, MaxOutputTokens: 8192,
			}
			writeTestProviders(t, dir, entry)
			cfg.LLM = LLMConfig{Provider: entry.Name, Model: entry.DefaultModel}
			a := New(cfg)
			t.Cleanup(func() { stopApp(t, a) })
			if err := boot.run(a); err != nil {
				t.Fatalf("%s boot: %v", boot.name, err)
			}
			if boot.safe {
				stopApp(t, a)
				if jobs, requests := discoveries.Load(), calls.Load(); jobs != 0 || requests != 0 {
					t.Fatalf("SAFE boot started %d discovery job(s) and asked the provider %d time(s)", jobs, requests)
				}
				return
			}
			select {
			case got := <-requested:
				if got.path != "/v1/models" || got.authorization != "Bearer synthetic-key" {
					t.Fatalf("selected provider discovery = %+v", got)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("declared token limits suppressed native capability discovery at live startup")
			}
		})
	}
}

func TestDelayedBootCapabilityDiscoveryPushesTheConnectedPagesConfig(t *testing.T) {
	beforeDiscovery, beforeGuard := selectedModelDiscovery, modalityGuard
	selectedModelDiscovery = (*App).discoverMetaForEntry
	t.Cleanup(func() {
		selectedModelDiscovery, modalityGuard = beforeDiscovery, beforeGuard
	})
	for _, catalogue := range []bool{false, true} {
		name := "native"
		if catalogue {
			name = "catalogue"
		}
		t.Run(name, func(t *testing.T) {
			const model = "fixture-author/delayed-capability-model"
			const cataloguePath = "/" + model + "/endpoints"
			requested, release := make(chan string, 1), make(chan struct{})
			t.Cleanup(func() {
				select {
				case <-release:
				default:
					close(release)
				}
			})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if catalogue && r.URL.Path == "/v1/models" {
					_, _ = w.Write([]byte(`{"data":[{"id":"` + model + `"}]}`))
					return
				}
				if (!catalogue && r.URL.Path != "/v1/models") || (catalogue && r.URL.Path != cataloguePath) {
					http.NotFound(w, r)
					return
				}
				select {
				case requested <- r.URL.Path:
				default:
				}
				select {
				case <-release:
				case <-r.Context().Done():
					return
				}
				if catalogue {
					_, _ = w.Write([]byte(`{"data":{"architecture":{"input_modalities":["text","image"]}}}`))
				} else {
					_, _ = w.Write([]byte(`{"data":[{"id":"` + model + `","capabilities":{"vision":true}}]}`))
				}
			}))
			t.Cleanup(server.Close)
			modalityGuard = func(_ context.Context, url string) error {
				if catalogue && (url == server.URL+cataloguePath || url == server.URL+"/") {
					return nil
				}
				return firewall.ErrEgressBlocked
			}
			dir := t.TempDir()
			keyPath, ledgerPath, dbPath := birthFixture(t, dir, "CapabilityPush")
			cfg := safebootConfig(t, dir, "CapabilityPush", keyPath, ledgerPath, dbPath)
			entry := providerEntry{
				Name: "fixture-provider", APIType: "openai", URL: server.URL + "/v1", Local: !catalogue,
				APIKey: "synthetic-key", DefaultModel: model, Default: true,
				ContextLength: 65536, MaxOutputTokens: 8192,
			}
			catalogueURL := ""
			if catalogue {
				catalogueURL = server.URL
			}
			raw, err := json.Marshal(providerRegistry{Providers: []providerEntry{entry}, ModelCatalogueURL: &catalogueURL})
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "providers.json"), raw, 0o600); err != nil {
				t.Fatal(err)
			}
			cfg.LLM = LLMConfig{Provider: entry.Name, Model: model}
			a := New(cfg)
			t.Cleanup(func() { stopApp(t, a) })
			if err := startLiveForTest(a); err != nil {
				t.Fatalf("live boot: %v", err)
			}
			select {
			case <-requested:
			case <-time.After(5 * time.Second):
				t.Fatal("startup never asked the capability source")
			}
			page := dialWSS(t, a.dashboard.Origin())
			sendQuery(t, page, "config")
			initial := readUntil(t, page, func(m *dashboard.ServerMessage) bool { return m.Type == "config" && m.Config != nil })
			if got := initial.Config.LLM.ImagesShown; got != "" {
				t.Fatalf("before the source answered images_shown=%q, want unknown", got)
			}
			close(release)
			readUntil(t, page, func(m *dashboard.ServerMessage) bool {
				return m.Type == "config" && m.Config != nil && m.Config.LLM.ImagesShown == "yes"
			})
		})
	}
}

func TestDelayedSelectionCapabilityPushesAfterAnEquallyCapableOldModel(t *testing.T) {
	before := selectedModelDiscovery
	selectedModelDiscovery = (*App).discoverMetaForEntry
	t.Cleanup(func() { selectedModelDiscovery = before })
	const oldModel, newModel = "fixture-old-vision", "fixture-new-vision"
	entered, release := make(chan struct{}), make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/models":
			close(entered) // refresh has already observed the old model's yes
			select {
			case <-release:
				_, _ = w.Write([]byte(`{"data":[{"id":"` + newModel + `","capabilities":{"vision":true}}]}`))
			case <-r.Context().Done():
			}
		case "/v1/chat/completions":
			select {
			case <-entered:
				_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"OK"},"finish_reason":"stop"}]}`))
			case <-r.Context().Done():
			}
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	dir := t.TempDir()
	entry := providerEntry{
		Name: "fixture-provider", APIType: "openai", URL: server.URL + "/v1", Local: true,
		APIKey: "synthetic-key", DefaultModel: oldModel, Default: true,
		ContextLength: 65536, MaxOutputTokens: 8192,
	}
	raw, err := json.Marshal(providerRegistry{
		Providers:         []providerEntry{entry},
		ModelCapabilities: map[string]modelCapability{oldModel: {Images: boolp(true)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "providers.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := defaultConfig()
	cfg.SourcePath, cfg.Tools.CWD = filepath.Join(dir, "config.json"), dir
	cfg.Identity = IdentityConfig{KeyPath: filepath.Join(dir, "identity.sec"), LedgerPath: filepath.Join(dir, "ledger.jsonl"), DBPath: filepath.Join(dir, "aii.db")}
	cfg.LLM = LLMConfig{Provider: entry.Name, Model: oldModel, Stream: boolp(false), TimeoutSeconds: 20, ProbeTimeoutSeconds: 20, Retries: -1}
	a := New(cfg)
	t.Cleanup(func() { stopApp(t, a) })
	cc, active, err := a.resolveLLM()
	if err != nil {
		t.Fatal(err)
	}
	client := llm.New(&cc)
	a.adoptRuntime(active, client)
	a.llmSwap = newSwappableLLM(client)
	a.dashboard = dashboard.New("127.0.0.1", 0, &dashboard.WSHandler{GetConfig: a.configState, SetConfig: a.applyConfigChange})
	if _, err := a.dashboard.Start(""); err != nil {
		t.Fatal(err)
	}
	page := dialWSS(t, a.dashboard.Origin())
	sendQuery(t, page, "config")
	initial := readUntil(t, page, func(m *dashboard.ServerMessage) bool { return m.Type == "config" && m.Config != nil })
	if got := initial.Config.LLM.ImagesShown; got != "yes" {
		t.Fatalf("old model images_shown=%q, want yes", got)
	}
	if err := wsjsonWrite(t.Context(), page, map[string]any{
		"type": "config_set", "request_id": "select-new-model", "config": map[string]any{"llm.model": newModel},
	}); err != nil {
		t.Fatal(err)
	}
	selected := readUntil(t, page, func(m *dashboard.ServerMessage) bool {
		if m.RequestID == "select-new-model" && m.Type == "error" {
			t.Fatalf("selection refused: %s", m.Message)
		}
		return m.RequestID == "select-new-model" && m.Type == "config" && m.Config != nil
	})
	if selected.Config.LLM.ResolvedModel != newModel || selected.Config.LLM.ImagesShown != "" || a.llmSwap.Current().ModelName() != newModel {
		t.Fatalf("selection did not activate the unknown candidate: %+v", selected.Config.LLM)
	}
	close(release)
	readUntil(t, page, func(m *dashboard.ServerMessage) bool {
		return m.Type == "config" && m.Config != nil && m.RequestID == "" && m.Config.LLM.ResolvedModel == newModel && m.Config.LLM.ImagesShown == "yes"
	})
}
