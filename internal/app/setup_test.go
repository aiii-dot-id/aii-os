package app

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/genesis"
	"github.com/aiii-dot-id/aii-os/internal/genesis/genesistest"
	"github.com/aiii-dot-id/aii-os/internal/llm"
)

// The Setup surface on a REAL app: config persists to disk, the LLM client
// swaps live (same adapter the conversation loop holds), secrets stay
// masked, and substrate-owned paths are rejected.
func TestSetupAppliesAndSwaps(t *testing.T) {
	// The use-door: a substrate change applies only if the candidate can
	// complete a real inference request.
	var inferenceRequests atomic.Int32
	fakeLLM := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/models") {
			w.Write([]byte(`{"data":[{"id":"m-v2"}]}`))
			return
		}
		inferenceRequests.Add(1)
		w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"OK"},"finish_reason":"stop"}]}`))
	}))
	defer fakeLLM.Close()

	dir := t.TempDir()
	// config home for saveConfig (writes ./config.json relative to CWD —
	// chdir into the temp dir so the test doesn't touch the repo)
	wd, _ := filepath.Abs(".")
	t.Chdir(dir)
	defer t.Chdir(wd)

	result := genesistest.NewRoot(t).Birth(t, genesis.BirthConfig{
		Name:       "SetupTest",
		KeyPath:    filepath.Join(dir, "identity.sec"),
		LedgerPath: filepath.Join(dir, "ledger.jsonl"),
		DBPath:     filepath.Join(dir, "aii.db"),
	})
	result.Ledger.Close()

	cfg := defaultConfig()
	cfg.Identity = IdentityConfig{
		Name: "SetupTest", KeyPath: filepath.Join(dir, "identity.sec"),
		LedgerPath: filepath.Join(dir, "ledger.jsonl"), DBPath: filepath.Join(dir, "aii.db"),
	}
	cfg.LLM = withTestProvider(t, dir, "old", "https://old.example", "m-v1", "sk-old-key-1234")
	cfg.Dashboard.Port = 0
	cfg.Tools.CWD = dir
	cfg.SourcePath = filepath.Join(dir, "config.json")
	app := New(cfg)
	if err := startLiveForTest(app); err != nil {
		t.Fatalf("startLive: %v", err)
	}
	defer app.Stop()

	// 1. Read: the pointer RESOLVES for display — provider data comes
	// from the providers.json entry, key masked, never the key.
	st := app.configState()
	if st.LLM.Provider != "old" || st.LLM.Endpoint != "https://old.example" {
		t.Fatalf("config state must show the resolved pointer, got %+v", st.LLM)
	}
	if st.LLM.APIKeyMasked != "••••1234" {
		t.Fatalf("key must be masked to last-4, got %q", st.LLM.APIKeyMasked)
	}
	if strings.Contains(st.LLM.APIKeyMasked, "sk-old") {
		t.Fatal("mask must not leak the key prefix")
	}

	// 2. Switch substrate: register the target provider (data lives in
	// providers.json — the one place), then move the POINTER. Applies
	// live — the adapter the loop holds swaps.
	if err := app.setProviderInfo(dashboard.ProviderInfo{
		Name: "new", Endpoint: fakeLLM.URL, APIKey: "sk-new-key-abcd", DefaultModel: "m-v2",
	}); err != nil {
		t.Fatal(err)
	}
	st2, err := app.applyConfigChange(map[string]interface{}{
		"llm.provider": "new", "llm.model": "m-v2",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(st2.RestartRequired) != 0 {
		t.Fatalf("llm changes apply live, restart_required = %v", st2.RestartRequired)
	}
	if app.llmSwap.Current().ModelName() != "m-v2" {
		t.Fatalf("the swappable client (what the loop uses) must carry the new model, got %s", app.llmSwap.Current().ModelName())
	}
	if st2.LLM.APIKeyMasked != "••••abcd" {
		t.Fatalf("resolved display must follow the new entry, got %q", st2.LLM.APIKeyMasked)
	}
	if st2.LLM.Provider != "new" || st2.LLM.Model != "m-v2" ||
		st2.LLM.ResolvedProvider != "new" || st2.LLM.ResolvedModel != "m-v2" {
		t.Fatalf("config pointer and resolution must both be reported: %+v", st2.LLM)
	}
	probes := inferenceRequests.Load()
	if err := app.setProviderInfo(dashboard.ProviderInfo{
		Name: "new", APIType: "openai", Endpoint: fakeLLM.URL, HasKey: true,
		DefaultModel: "m-v2", ConfiguredModels: []string{"m-v2", "m-alias"},
	}); err != nil {
		t.Fatalf("catalogue-only active-provider edit: %v", err)
	}
	if got := inferenceRequests.Load(); got != probes {
		t.Fatalf("catalogue-only edit made %d inference probes, want 0", got-probes)
	}

	// 2b. A catalogue is not a functioning substrate. This is the exact
	// live regression: /models listed the requested model, then every chat
	// request returned an account/quota 429. The switch must be refused
	// before config, provenance, or the live client moves.
	listedDead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/models") {
			w.Write([]byte(`{"data":[{"id":"listed-dead"}]}`))
			return
		}
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"error":{"message":"insufficient balance"}}`))
	}))
	defer listedDead.Close()
	if err := app.setProviderInfo(dashboard.ProviderInfo{
		Name: "listed-dead", Endpoint: listedDead.URL, APIKey: "sk-dead", DefaultModel: "listed-dead",
	}); err != nil {
		t.Fatal(err)
	}
	app.cfgMu.Lock()
	app.cfg.LLM.Retries = -1 // this test exercises refusal, not retry timing
	app.cfgMu.Unlock()
	seqBefore := app.ledger.LastSeq()
	_, err = app.applyConfigChange(map[string]interface{}{
		"llm.provider": "listed-dead", "llm.model": "listed-dead",
	})
	if err == nil || !strings.Contains(err.Error(), "cannot complete a minimal inference") || !strings.Contains(err.Error(), "429") {
		t.Fatalf("listed-but-dead substrate must be refused with the inference reason, got %v", err)
	}
	current := app.configSnapshot()
	if current.LLM.Provider != "new" || current.LLM.Model != "m-v2" || app.llmSwap.Current().ModelName() != "m-v2" {
		t.Fatalf("failed probe replaced the working substrate: cfg=%+v client=%s", current.LLM, app.llmSwap.Current().ModelName())
	}
	if app.ledger.LastSeq() != seqBefore {
		t.Fatalf("substrate probe must never mint: seq %d -> %d", seqBefore, app.ledger.LastSeq())
	}

	// A retryable subscription throttle is still not a completed inference.
	// The typed retry guidance survives in the refusal, but a working client
	// is never replaced by a route that has not produced an answer.
	throttled := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"type":"error","error":{"type":"rate_limit_error","message":"Error"}}`))
	}))
	defer throttled.Close()
	subscriptionToken := filepath.Join(dir, "subscription-token.json")
	if err := os.WriteFile(subscriptionToken, []byte(`{"access_token":"oauth-token"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := app.setProviderInfo(dashboard.ProviderInfo{
		Name: "subscription", APIType: "anthropic", Endpoint: throttled.URL,
		Credential: "file:" + subscriptionToken, DefaultModel: "claude-opus-5",
	}); err != nil {
		t.Fatal(err)
	}
	_, err = app.applyConfigChange(map[string]interface{}{
		"llm.provider": "subscription", "llm.model": "claude-opus-5",
	})
	if err == nil || !strings.Contains(err.Error(), "cannot complete a minimal inference") || !strings.Contains(err.Error(), "429") {
		t.Fatalf("retryable subscription throttle must refuse unproved inference, got %v", err)
	}
	if current := app.configSnapshot(); current.LLM.Provider != "new" || current.LLM.Model != "m-v2" || app.llmSwap.Current().ModelName() != "m-v2" {
		t.Fatalf("retryable throttle replaced the working substrate: cfg=%+v client=%s", current.LLM, app.llmSwap.Current().ModelName())
	}
	if app.ledger.LastSeq() != seqBefore {
		t.Fatalf("retryable substrate refusal minted: seq %d -> %d", seqBefore, app.ledger.LastSeq())
	}

	// 2c. Editing the selected provider changes the resolved substrate
	// even though the pointer text stays the same. The same use-door must
	// refuse a dead endpoint before providers.json or the client moves.
	if err := app.setProviderInfo(dashboard.ProviderInfo{
		Name: "new", Endpoint: listedDead.URL, APIKey: "sk-new-key-abcd", DefaultModel: "m-v2",
	}); err == nil || !strings.Contains(err.Error(), "cannot complete a minimal inference") {
		t.Fatalf("dead active-provider edit must be refused, got %v", err)
	}
	reg, err := app.loadProviders()
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range reg.Providers {
		if entry.Name == "new" && entry.URL != fakeLLM.URL {
			t.Fatalf("refused provider edit reached disk: %q", entry.URL)
		}
	}
	if app.llmSwap.Current().ModelName() != "m-v2" {
		t.Fatalf("refused provider edit replaced the live client: %s", app.llmSwap.Current().ModelName())
	}
	if err := app.deleteProvider("new"); err == nil || !strings.Contains(err.Error(), "is not in") {
		t.Fatalf("deleting the selected provider must be refused, got %v", err)
	}
	fileEdit := app.configSnapshot()
	fileEdit.LLM.Provider, fileEdit.LLM.Model = "listed-dead", "listed-dead"
	if _, err := saveConfig(&fileEdit); err != nil {
		t.Fatal(err)
	}
	app.reloadConfig()
	if current := app.configSnapshot(); current.LLM.Provider != "new" || app.llmSwap.Current().ModelName() != "m-v2" {
		t.Fatalf("dead file reload replaced the working substrate: cfg=%+v client=%s", current.LLM, app.llmSwap.Current().ModelName())
	}

	// 3. Witness change: saved, restart-required, honestly reported
	st3, err := app.applyConfigChange(map[string]interface{}{"witness.url": "https://witness.example"})
	if err != nil {
		t.Fatal(err)
	}
	if len(st3.RestartRequired) == 0 || st3.RestartRequired[0] != "witness.url" {
		t.Fatalf("witness change must be reported restart-required, got %v", st3.RestartRequired)
	}

	// 4. Substrate-owned paths: rejected
	for _, path := range []string{"identity.ledger_path", "identity.key_path", "tools.cwd", "evil.unknown"} {
		if _, err := app.applyConfigChange(map[string]interface{}{path: "/tmp/x"}); err == nil {
			t.Fatalf("%s must be rejected", path)
		}
	}

	// 4b. Dashboard bind AND the HTTPS choice are operator-editable,
	// restart-required. The socket serving this UI cannot rebind under
	// itself, and it cannot change scheme under itself either.
	stD, err := app.applyConfigChange(map[string]interface{}{"dashboard.host": "0.0.0.0", "dashboard.port": 9090, "dashboard.tls": true})
	if err != nil {
		t.Fatalf("dashboard.host/port/tls are operator-settable: %v", err)
	}
	if len(stD.RestartRequired) != 3 {
		t.Fatalf("dashboard bind and scheme changes are restart-required, got %v", stD.RestartRequired)
	}
	if dashboardCfg := app.configSnapshot().Dashboard; dashboardCfg.Host != "0.0.0.0" || dashboardCfg.Port != 9090 || !dashboardCfg.TLS {
		t.Fatalf("dashboard bind/scheme not applied to config: %+v", dashboardCfg)
	}
	// A checkbox is a boolean. Anything else is a crafted message, and
	// coercing it would turn a typo into a silent downgrade to plaintext.
	if _, err := app.applyConfigChange(map[string]interface{}{"dashboard.tls": "true"}); err == nil {
		t.Fatal("dashboard.tls accepted a string; a non-boolean must be refused, not coerced")
	}

	// 5. Persisted: the saved config carries the POINTER — never
	// provider data (that stays in providers.json).
	saved, err := LoadConfig(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatalf("saved config: %v", err)
	}
	info, err := os.Stat(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("config.json mode = %o, want 600", info.Mode().Perm())
	}
	if saved.LLM.Provider != "new" || saved.LLM.Model != "m-v2" || saved.Witness.URL != "https://witness.example" {
		t.Fatalf("saved config must carry applied changes: %+v %+v", saved.LLM, saved.Witness)
	}

	// 6. The DELETED config fields are gone from the whitelist too —
	// provider data travels through provider_set, and a crafted
	// config_set naming the old keys is rejected, never silently
	// absorbed.
	for _, gone := range []string{"llm.endpoint", "llm.api_key", "llm.thinking_budget", "llm.max_output_tokens", "llm.context_length", "llm.reasoning_effort"} {
		if _, err := app.applyConfigChange(map[string]interface{}{gone: "x"}); err == nil {
			t.Fatalf("%s is provider data now — config_set must reject it", gone)
		}
	}

	// 7. Bad values rejected: dangling pointer (refused BEFORE persist —
	// the file must keep the working substrate), bad timeout, bad tz.
	if _, err := app.applyConfigChange(map[string]interface{}{"llm.provider": "ghost"}); err == nil {
		t.Fatal("a pointer at no entry must be rejected")
	}
	if after, _ := LoadConfig(filepath.Join(dir, "config.json")); after.LLM.Provider != "new" {
		t.Fatalf("a refused substrate must not linger in the file, got %q", after.LLM.Provider)
	}
	if _, err := app.applyConfigChange(map[string]interface{}{"llm.timeout_seconds": -5}); err == nil {
		t.Fatal("negative timeout must be rejected")
	}
	if _, err := app.applyConfigChange(map[string]interface{}{"dashboard.port": 8180.5}); err == nil {
		t.Fatal("fractional integer field must be rejected")
	}
	if _, err := app.applyConfigChange(map[string]interface{}{"timezone": "Mars/Olympus"}); err == nil {
		t.Fatal("unknown timezone must be rejected")
	}
}

// Auth-required discovery: 401-without-key is GUIDANCE (the form says
// "enter a key"), 401-with-key says the key was rejected — never a raw
// status in the birth form.
func TestDiscoverAuthRequiredIsGuidance(t *testing.T) {
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
	}))
	defer fake.Close()
	if _, err := discoverModels(context.Background(), fake.URL, ""); err == nil || !strings.Contains(err.Error(), "requires an API key") {
		t.Fatalf("keyless 401 must be auth-required guidance, got: %v", err)
	}
	if _, err := discoverModels(context.Background(), fake.URL, "sk-x"); err == nil || !strings.Contains(err.Error(), "key rejected") {
		t.Fatalf("keyed 401 must say the key was rejected, got: %v", err)
	}
}

func TestConfigStatePreservesDefaultPointerAndReportsResolution(t *testing.T) {
	dir := t.TempDir()
	writeTestProviders(t, dir, providerEntry{
		Name: "default-provider", URL: "https://provider.example/v1",
		DefaultModel: "default-model", Default: true,
	})
	a := resolverApp(dir, LLMConfig{})
	st := a.configState()
	if st.LLM.Provider != "" || st.LLM.Model != "" {
		t.Fatalf("reading config state must not replace the empty default pointer: %+v", st.LLM)
	}
	if st.LLM.ResolvedProvider != "default-provider" || st.LLM.ResolvedModel != "default-model" {
		t.Fatalf("resolved substrate must still be visible separately: %+v", st.LLM)
	}
}

func TestSubstrateProbeDoesNotHoldConfigLock(t *testing.T) {
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseProbe := func() { releaseOnce.Do(func() { close(release) }) }
	defer releaseProbe()
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		started <- struct{}{}
		<-release
		w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"OK"}}]}`))
	}))
	defer fake.Close()

	dir := t.TempDir()
	writeTestProviders(t, dir,
		providerEntry{Name: "old", URL: "https://old.example", DefaultModel: "m1", Default: true},
		providerEntry{Name: "new", URL: fake.URL, DefaultModel: "m2"},
	)
	cfg := defaultConfig()
	cfg.LLM = LLMConfig{Provider: "old", Model: "m1", TimeoutSeconds: 5, Retries: -1}
	cfg.SourcePath = filepath.Join(dir, "config.json")
	if _, err := saveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	a := New(cfg)
	a.bgCtx = t.Context()

	done := make(chan error, 1)
	go func() {
		_, err := a.applyConfigChange(map[string]interface{}{"llm.provider": "new", "llm.model": "m2"})
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("candidate probe did not start")
	}

	state := make(chan *dashboard.ConfigState, 1)
	go func() { state <- a.configState() }()
	select {
	case got := <-state:
		if got.LLM.Provider != "old" {
			t.Fatalf("uncommitted candidate became visible: %+v", got.LLM)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("config read blocked behind the LLM probe")
	}
	releaseProbe()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestSubstrateChangeWorksBeforeLiveModeSelection(t *testing.T) {
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"OK"}}]}`))
	}))
	defer fake.Close()

	dir := t.TempDir()
	writeTestProviders(t, dir,
		providerEntry{Name: "old", URL: "https://old.example", DefaultModel: "m1", Default: true},
		providerEntry{Name: "new", URL: fake.URL, DefaultModel: "m2"},
	)
	cfg := defaultConfig()
	cfg.LLM = LLMConfig{Provider: "old", Model: "m1", Retries: -1}
	cfg.SourcePath = filepath.Join(dir, "config.json")
	if _, err := saveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	a := New(cfg)
	defer a.Stop()
	a.llmSwap = newSwappableLLM(llm.New(&llm.ClientConfig{Endpoint: "https://old.example", Model: "m1"}))

	if _, err := a.applyConfigChange(map[string]interface{}{
		"llm.provider": "new", "llm.model": "m2",
	}); err != nil {
		t.Fatalf("pre-LIVE substrate repair: %v", err)
	}
	if got := a.llmSwap.Current().ModelName(); got != "m2" {
		t.Fatalf("active model = %q, want m2", got)
	}
}

func TestPublishedConfigFollowsDiskWhenDirectorySyncFails(t *testing.T) {
	cfg := defaultConfig()
	cfg.SourcePath = filepath.Join(t.TempDir(), "config.json")
	if _, err := saveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	a := New(cfg)
	syncErr := errors.New("injected directory sync failure")
	persist := func(candidate *Config) (bool, error) {
		published, err := saveConfig(candidate)
		if err != nil {
			return published, err
		}
		return true, syncErr
	}

	_, err := a.applyConfigChangeWith(map[string]interface{}{"updates.automatic": true}, persist)
	if err == nil || !strings.Contains(err.Error(), "published and applied live") || !errors.Is(err, syncErr) {
		t.Fatalf("got %v, want honest post-publication durability error", err)
	}
	if !a.configSnapshot().Updates.Automatic {
		t.Fatal("live config did not follow the published file")
	}
	onDisk, err := LoadConfig(cfg.SourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if !onDisk.Updates.Automatic {
		t.Fatal("candidate was not published to disk")
	}
}

func TestConfigPersistenceFailureLeavesMemoryUnchanged(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "not-a-directory")
	if err := os.WriteFile(blocker, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := &Config{SourcePath: filepath.Join(blocker, "config.json")}
	a := New(cfg)
	if _, err := a.applyConfigChange(map[string]interface{}{"witness.url": "https://witness.example"}); err == nil {
		t.Fatal("persistence failure reported success")
	}
	if cfg.Witness.URL != "" {
		t.Fatalf("failed persistence changed live config: %q", cfg.Witness.URL)
	}
}

// The visible-output floor now lives in the resolver (the budgets are
// provider-entry data): see TestResolveLLMVisibleFloorClamp.

// The checkbox has to actually reach the listener, and it reaches it as
// ONE value: tlsDirFor returns "" when HTTPS is unchecked, which Start
// reads as "serve plaintext". A tlsDirFor that ignores the flag is the
// worst shape this bug can take — the box saves, redisplays checked, and
// changes nothing, so the operator believes they have TLS and does not.
func TestTLSDirectoryFollowsTheHTTPSChoice(t *testing.T) {
	var cfg Config
	cfg.Identity.LedgerPath = filepath.Join(t.TempDir(), "ledger.db")

	cfg.Dashboard.TLS = false
	if d := tlsDirFor(cfg); d != "" {
		t.Errorf("HTTPS unchecked but tlsDirFor returned %q — Start would serve TLS regardless and the checkbox would be inert", d)
	}

	cfg.Dashboard.TLS = true
	if d := tlsDirFor(cfg); d == "" {
		t.Error("HTTPS checked but tlsDirFor returned empty — Start would serve plaintext while the operator believes it is encrypted")
	}
}
