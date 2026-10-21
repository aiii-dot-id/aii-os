package app

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/limits"
	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
)

// THE RESIDENT'S CALL CEILING DEFAULTS TO UNLIMITED (agency.max_tool_calls):
// the identity has always run with none, and a home that sets nothing runs
// so still, with nothing written for it in config.json.
func TestTheResidentsCallCeilingDefaultsToUnlimited(t *testing.T) {
	c := *defaultConfig()
	if got := maxToolCallsSetting.value(c); got != limits.Unlimited {
		t.Fatalf("the resident's call ceiling with nothing set is %s", got)
	}
	if b := limitsFor(c, providerEntry{Name: "any"}, "m").residentBounds(); b.Calls != limits.Unlimited {
		t.Fatalf("the resident is bounded at %s calls with nothing set", b.Calls)
	}
	raw, err := json.Marshal(c)
	if err != nil || strings.Contains(string(raw), `"max_tool_calls"`) {
		t.Fatalf("an unset call ceiling is written into config.json: %v", err)
	}
	app, _ := dispatchApp(t)
	defer stopApp(t, app)
	if held := agencyHeld(app); held.TurnRounds != limits.Of(30) {
		t.Fatalf("rig: the resident began at %s rounds", held.TurnRounds)
	}
	if got := loopCalls(app); got != limits.Unlimited {
		t.Fatalf("the booted resident holds %s calls; want no limit", got)
	}
	if _, err := app.applyConfigChange(map[string]any{"agency.max_tool_calls": float64(5)}); err != nil {
		t.Fatal(err)
	}
	if got := loopCalls(app); got != limits.Of(5) {
		t.Fatalf("after the door set 5 the resident holds %s calls", got)
	}
}

// loopCalls is the call ceiling the resident's loop holds for its next turn.
func loopCalls(a *App) limits.Ceiling { return agencyHeld(a).TurnCalls }

// SWITCHING TO A LOCAL MODEL LIFTS THE TURN FENCE AT THE NEXT TURN:
// the overrides of the entry the identity is switched to are put in
// force with it, and switching away puts the main setting back.
func TestSwitchingToALocalModelLiftsTheTurnFenceAtTheNextTurn(t *testing.T) {
	app, _ := dispatchApp(t)
	defer stopApp(t, app)
	cfg := app.configSnapshot()
	cfg.Overrides = configWith(t, `{"Local server":{"agency":{"turn_token_budget":"unlimited","max_tool_calls":"unlimited"},"models":{"big":{"agency":{"max_tool_rounds":200}}}}}`).Overrides
	local := providerEntry{Name: "Local server", Local: true, URL: "http://127.0.0.1:1/v1", APIType: "openai"}
	client := llm.New(&llm.ClientConfig{Endpoint: local.URL, Provider: "openai", Model: "big"})
	if err := app.acquireTurn(t.Context()); err != nil {
		t.Fatal(err)
	}
	app.activateLLMRuntime(client, local, cfg)
	app.releaseTurn()
	if held := agencyHeld(app); held.TurnTokens != limits.Unlimited || held.TurnRounds != limits.Of(200) {
		t.Fatalf("on the local model the resident holds a fence of %s and %s rounds; want unlimited and the model's 200", held.TurnTokens, held.TurnRounds)
	}
	back := llm.New(&llm.ClientConfig{Endpoint: "https://x", Provider: "openai", Model: "m"})
	if err := app.acquireTurn(t.Context()); err != nil {
		t.Fatal(err)
	}
	app.activateLLMRuntime(back, providerEntry{Name: "test", URL: "https://x", APIType: "openai"}, cfg)
	app.releaseTurn()
	if held := agencyHeld(app); held.TurnTokens != limits.Of(600_000) || held.TurnRounds != limits.Of(30) {
		t.Fatalf("switched back, the resident holds a fence of %s and %s rounds; want the main settings'", held.TurnTokens, held.TurnRounds)
	}
}

// A PROVIDER'S TRANSPORT OVERRIDE REACHES EVERY CLIENT BUILT FOR IT: its
// timeout, retries and streaming are the server's, and another provider's
// clients keep the main settings'.
func TestAProvidersTransportOverrideReachesItsClient(t *testing.T) {
	dir := t.TempDir()
	llmCfg := withTestProvider(t, dir, "Slow", "http://127.0.0.1:1/v1", "m", "k")
	a := resolverApp(dir, llmCfg)
	t.Cleanup(a.bgCancel)
	reg, err := a.loadProviders()
	if err != nil {
		t.Fatal(err)
	}
	reg.Providers = append(reg.Providers, providerEntry{Name: "Fast", APIType: "openai", URL: "http://127.0.0.1:2/v1", APIKey: "k", DefaultModel: "m"})
	full := Config{LLM: llmCfg, Overrides: configWith(t, `{"Slow":{"llm":{"timeout_seconds":600,"retries":-1,"retry_backoff_ms":50,"stream":false}}}`).Overrides}
	cc, _, err := a.resolveLLMConfig(full, reg)
	if err != nil {
		t.Fatal(err)
	}
	if cc.TimeoutSeconds != 600 || cc.Retries != -1 || cc.RetryBackoffMS != 50 || !cc.NoStream {
		t.Fatalf("the slow server's client: timeout %d, retries %d, backoff %d, no stream %v", cc.TimeoutSeconds, cc.Retries, cc.RetryBackoffMS, cc.NoStream)
	}
	if cc, _, err := a.resolveRouteLLM(full, reg, "Fast", "m"); err != nil || cc.TimeoutSeconds != 120 || cc.Retries != 1 || cc.NoStream {
		t.Fatalf("another provider's client took the override: timeout %d, retries %d, no stream %v, %v", cc.TimeoutSeconds, cc.Retries, cc.NoStream, err)
	}
	if cc, _, err := a.resolveRouteLLM(full, reg, "Slow", "m"); err != nil || cc.TimeoutSeconds != 600 {
		t.Fatalf("a route to the slow server did not take its override: %d, %v", cc.TimeoutSeconds, err)
	}
}

// THE TURN'S LIMITS ARE SAID WHERE AN OVERRIDE SET ONE: the provider and
// model, and each of the three spend limits with where it came from; a home
// with no overrides is told nothing new.
func TestTheTurnLimitsLineNamesWhereEachLimitCameFrom(t *testing.T) {
	cfg := configWith(t, `{"Local server":{"agency":{"turn_token_budget":"unlimited"}}}`)
	l := limitsFor(cfg, providerEntry{Name: "Local server"}, "q")
	want := "TURN LIMITS: Local server: tokens unlimited (provider), rounds 30 (main), calls unlimited (default)"
	if got := l.turnLimitsLine(); got != want {
		t.Fatalf("the line is %q; want %q", got, want)
	}
	if got := limitsFor(*defaultConfig(), providerEntry{Name: "any"}, "m").turnLimitsLine(); got != "" {
		t.Fatalf("a home with no overrides is told %q", got)
	}
	app, _ := dispatchApp(t)
	defer stopApp(t, app)
	logs := logsink.CaptureForTest(t)
	app.applyResidentLimits(l)
	if !strings.Contains(logs.String(), want) {
		t.Fatalf("applying the profile did not say it:\n%s", logs.String())
	}
}
