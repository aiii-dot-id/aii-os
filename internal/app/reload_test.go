package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/genesis"
	"github.com/aiii-dot-id/aii-os/internal/genesis/genesistest"
	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/ring"
)

// A sandbox grant must be LIVE end to end (2026-08-18, James): the
// registry widens, AND the identity's Ring 5 floor names the granted
// root — enforcement and knowledge move together, no restart. Exercised
// through reloadConfig, the shared path behind the file watcher, SIGHUP,
// and (for the floor half) the dashboard grant.
func TestReloadAppliesSandboxRootsLive(t *testing.T) {
	dir := t.TempDir()
	result := genesistest.NewRoot(t).Birth(t, genesis.BirthConfig{
		Name:       "ReloadTest",
		KeyPath:    filepath.Join(dir, "identity.sec"),
		LedgerPath: filepath.Join(dir, "ledger.jsonl"),
		DBPath:     filepath.Join(dir, "aii.db"),
	})
	result.Ledger.Close()

	cfgPath := filepath.Join(dir, "config.json")
	cfg := defaultConfig()
	cfg.Identity = IdentityConfig{
		Name: "ReloadTest", KeyPath: filepath.Join(dir, "identity.sec"),
		LedgerPath: filepath.Join(dir, "ledger.jsonl"), DBPath: filepath.Join(dir, "aii.db"),
	}
	cfg.LLM = withTestProvider(t, dir, "test", "https://x", "m", "sk-x")
	cfg.Dashboard.Port = 0
	cfg.Tools.CWD = dir
	cfg.SourcePath = cfgPath
	b, _ := json.Marshal(cfg)
	if err := os.WriteFile(cfgPath, b, 0o644); err != nil {
		t.Fatal(err)
	}

	app := New(cfg)
	if err := startLiveForTest(app); err != nil {
		t.Fatalf("startLive: %v", err)
	}
	defer app.Stop()

	granted := t.TempDir()
	if err := os.WriteFile(filepath.Join(granted, "hello.txt"), []byte("reachable"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Simulate an operator editing config.json by hand.
	cfg2 := *cfg
	cfg2.Tools.ExtraRoots = []string{granted}
	b2, _ := json.Marshal(&cfg2)
	if err := os.WriteFile(cfgPath, b2, 0o644); err != nil {
		t.Fatal(err)
	}

	app.reloadConfig()

	// Enforcement: reachable, live.
	res, err := app.toolReg.Execute(t.Context(), "read", map[string]interface{}{"file_path": filepath.Join(granted, "hello.txt")})
	if err != nil || res.Error != "" || !strings.Contains(res.Output, "reachable") {
		t.Fatalf("granted root must be readable after reload: %v %+v", err, res)
	}

	// Knowledge: her floor names the root.
	floor := app.rings.GetContent(ring.Ring5)
	if !strings.Contains(floor, granted) {
		t.Fatalf("Ring 5 floor must name the granted root — enforcement without knowledge is the bug this fixes")
	}
	if !strings.Contains(floor, "Granted roots") {
		t.Fatal("floor must carry the granted-roots section")
	}
}

func TestReloadDoesNotActivateSupersededFile(t *testing.T) {
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		started <- struct{}{}
		<-release
		w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"OK"}}]}`))
	}))
	defer server.Close()

	dir := t.TempDir()
	writeTestProviders(t, dir,
		providerEntry{Name: "old", URL: "https://old.example", DefaultModel: "m1", Default: true},
		providerEntry{Name: "new", URL: server.URL, DefaultModel: "m2"},
	)
	cfg := defaultConfig()
	cfg.LLM = LLMConfig{Provider: "old", Model: "m1", TimeoutSeconds: 5, Retries: -1}
	cfg.SourcePath = filepath.Join(dir, "config.json")
	if _, err := saveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	a := New(cfg)
	a.live = true
	a.bgCtx = t.Context()
	a.llmSwap = newSwappableLLM(llm.New(&llm.ClientConfig{Model: "m1"}))

	candidate := *cfg
	candidate.LLM.Provider, candidate.LLM.Model = "new", "m2"
	if _, err := saveConfig(&candidate); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		a.reloadConfig()
		close(done)
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("reload probe did not start")
	}

	newer := candidate
	newer.LLM.Model = "m3"
	if _, err := saveConfig(&newer); err != nil {
		t.Fatal(err)
	}
	close(release)
	<-done
	if got := a.configSnapshot().LLM.Model; got != "m1" {
		t.Fatalf("superseded reload activated %q", got)
	}
	if got := a.llmSwap.Current().ModelName(); got != "m1" {
		t.Fatalf("superseded reload swapped client to %q", got)
	}
	persisted, err := LoadConfig(cfg.SourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.LLM.Model != "m3" {
		t.Fatalf("reload overwrote newer file with %q", persisted.LLM.Model)
	}
}

func TestReloadTransportTuningDoesNotProbe(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		http.Error(w, "provider unavailable", http.StatusTooManyRequests)
	}))
	defer server.Close()

	dir := t.TempDir()
	writeTestProviders(t, dir, providerEntry{
		Name: "provider", URL: server.URL, APIKey: "key", DefaultModel: "model", Default: true,
	})
	cfg := defaultConfig()
	cfg.LLM = LLMConfig{Provider: "provider", Model: "model", TimeoutSeconds: 5, Retries: -1}
	cfg.SourcePath = filepath.Join(dir, "config.json")
	if _, err := saveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	a := New(cfg)
	a.live = true
	a.bgCtx = t.Context()
	a.llmSwap = newSwappableLLM(llm.New(&llm.ClientConfig{Model: "model"}))

	edited := *cfg
	edited.LLM.TimeoutSeconds = 10
	if _, err := saveConfig(&edited); err != nil {
		t.Fatal(err)
	}
	a.reloadConfig()

	if got := requests.Load(); got != 0 {
		t.Fatalf("timeout-only reload made %d inference requests", got)
	}
	if got := a.configSnapshot().LLM.TimeoutSeconds; got != 10 {
		t.Fatalf("timeout = %d, want 10", got)
	}
}

func TestReloadPreservesRestartEditOnNextSave(t *testing.T) {
	dir := t.TempDir()
	cfg := defaultConfig()
	cfg.SourcePath = filepath.Join(dir, "config.json")
	if _, err := saveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	a := New(cfg)
	a.live = true

	edited := *cfg
	edited.Dashboard.Port = 9191
	if _, err := saveConfig(&edited); err != nil {
		t.Fatal(err)
	}
	a.reloadConfig()
	if got := a.configSnapshot().Dashboard.Port; got != 9191 {
		t.Fatalf("restart-required file edit was not retained in desired config: %d", got)
	}
	if _, err := a.applyConfigChange(map[string]interface{}{"updates.automatic": true}); err != nil {
		t.Fatal(err)
	}
	persisted, err := LoadConfig(cfg.SourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Dashboard.Port != 9191 {
		t.Fatalf("later save overwrote restart-required edit with port %d", persisted.Dashboard.Port)
	}
}
