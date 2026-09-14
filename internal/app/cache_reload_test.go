package app

import (
	"encoding/json"
	"errors"
	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/prompt"
	"github.com/aiii-dot-id/aii-os/internal/ring"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
)

type pass5Fixture struct {
	a     *App
	dir   string
	entry providerEntry
	mu    sync.Mutex
	last  map[string]any
}

func pass5New(t *testing.T) *pass5Fixture {
	t.Helper()
	f := &pass5Fixture{dir: t.TempDir()}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"id": "gpt-5.2"}}})
			return
		}
		var req map[string]any
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			return
		}
		f.mu.Lock()
		f.last = req
		f.mu.Unlock()
		msg := map[string]any{"role": "assistant", "content": "OK"}
		reason := "stop"
		if raw, ok := req["tools"].([]any); ok && len(raw) > 0 {
			reason = "tool_calls"
			msg["tool_calls"] = []any{map[string]any{"id": "fixture-call", "type": "function", "function": map[string]any{"name": "report_ready", "arguments": `{"ready":true}`}}}
		}
		json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": msg, "finish_reason": reason}}, "usage": map[string]any{"prompt_tokens": 10, "completion_tokens": 2, "total_tokens": 12, "prompt_tokens_details": map[string]any{"cached_tokens": 0}}})
	}))
	t.Cleanup(srv.Close)
	f.entry = providerEntry{Name: "Cache Review", APIType: "openai", URL: srv.URL, APIKey: "fixture", DefaultModel: "gpt-5.2", Default: true, ContextLength: 64000, MaxOutputTokens: 512, Extra: map[string]any{"prompt_cache_key": "old-key", "prompt_cache_retention": "in_memory"}}
	writeTestProviders(t, f.dir, f.entry)
	cfg := defaultConfig()
	cfg.LLM = LLMConfig{Provider: f.entry.Name, Model: f.entry.DefaultModel, TimeoutSeconds: 3, Retries: -1}
	cfg.SourcePath = filepath.Join(f.dir, "config.json")
	if _, err := saveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	normalized, err := LoadConfig(cfg.SourcePath)
	if err != nil {
		t.Fatal(err)
	}
	f.a = New(normalized)
	t.Cleanup(f.a.bgCancel)
	f.a.live = true
	f.a.composer = prompt.New(ring.NewManager(), 32000)
	cc, e, err := f.a.resolveLLM()
	if err != nil {
		t.Fatal(err)
	}
	f.a.llmSwap = newSwappableLLM(f.a.newLLMClient(cc, 32000))
	f.a.activeProvider = e
	return f
}
func (f *pass5Fixture) wire(t *testing.T) map[string]any {
	t.Helper()
	if _, err := f.a.llmSwap.Chat(t.Context(), []llm.Message{{Role: "user", Content: "check wire"}}, llm.ChatOptions{}); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.last
}
func (f *pass5Fixture) info(extra map[string]any) dashboard.ProviderInfo {
	return dashboard.ProviderInfo{Name: f.entry.Name, APIType: "openai", Endpoint: f.entry.URL, APIKey: "fixture", DefaultModel: f.entry.DefaultModel, Default: true, ContextLength: 64000, MaxOutputTokens: 512, Extra: extra}
}
func TestCacheFileEditAndReloadReachActiveRequest(t *testing.T) {
	f := pass5New(t)
	if got := f.wire(t)["prompt_cache_key"]; got != "old-key" {
		t.Fatalf("fixture initial key=%v", got)
	}
	next := f.entry
	next.Extra = map[string]any{"prompt_cache_key": "new-key", "prompt_cache_retention": "24h"}
	writeTestProviders(t, f.dir, next)
	f.a.reloadConfig()
	cc, _, err := f.a.resolveLLM()
	if err != nil {
		t.Fatal(err)
	}
	got := f.wire(t)
	if cc.Extra["prompt_cache_key"] != "new-key" {
		t.Fatal("file edit did not reach loader")
	}
	if got["prompt_cache_key"] != "new-key" || got["prompt_cache_retention"] != "24h" {
		t.Fatalf("loader resolves new-key/24h but active request after reload sends %v/%v", got["prompt_cache_key"], got["prompt_cache_retention"])
	}
}
func TestCacheSavingAlreadyEditedEntryReconcilesRuntime(t *testing.T) {
	f := pass5New(t)
	extra := map[string]any{"prompt_cache_key": "new-key", "prompt_cache_retention": "24h"}
	next := f.entry
	next.Extra = extra
	writeTestProviders(t, f.dir, next)
	f.a.reloadConfig()
	if err := f.a.setProviderInfo(f.info(extra)); err != nil {
		t.Fatal(err)
	}
	if got := f.wire(t)["prompt_cache_key"]; got != "new-key" {
		t.Fatalf("saving the already-edited provider returned success but active request still uses %v", got)
	}
}
func TestCacheManagedCacheEditAndClearReachWire(t *testing.T) {
	f := pass5New(t)
	extra := map[string]any{"prompt_cache_key": "managed-key", "prompt_cache_retention": "24h"}
	if err := f.a.setProviderInfo(f.info(extra)); err != nil {
		t.Fatal(err)
	}
	got := f.wire(t)
	if got["prompt_cache_key"] != "managed-key" || got["prompt_cache_retention"] != "24h" {
		t.Fatalf("managed edit did not reach active request: %v/%v", got["prompt_cache_key"], got["prompt_cache_retention"])
	}
	if err := f.a.setProviderInfo(f.info(nil)); err != nil {
		t.Fatal(err)
	}
	got = f.wire(t)
	if _, exists := got["prompt_cache_key"]; exists {
		t.Fatal("cleared cache key still sent")
	}
	if _, exists := got["prompt_cache_retention"]; exists {
		t.Fatal("cleared retention still sent")
	}
	t.Log("managed provider edit activates cache key/retention; clearing removes both from the next request")
}

func TestCacheModelCapabilityFileEditReachesRuntime(t *testing.T) {
	legacy, modern := false, true
	f := pass5New(t)
	reg, err := loadProvidersFile(f.a.providersPath())
	if err != nil {
		t.Fatal(err)
	}
	if reg.ModelCapabilities == nil {
		reg.ModelCapabilities = map[string]modelCapability{}
	}
	reg.ModelCapabilities["gpt-5.2"] = modelCapability{Effort: []string{}, MaxCompletionTokens: &legacy, CacheRetentions: []string{"in_memory", "24h"}}
	if _, err := saveProvidersFile(f.a.providersPath(), reg); err != nil {
		t.Fatal(err)
	}
	f.a.reloadConfig()
	got := f.wire(t)
	if _, ok := got["max_tokens"]; !ok {
		t.Fatal("changed model wire capability did not reach the client")
	}
	reg.ModelCapabilities["gpt-5.2"] = modelCapability{Effort: []string{}, MaxCompletionTokens: &modern, CacheRetentions: []string{"in_memory", "24h"}}
	if _, err := saveProvidersFile(f.a.providersPath(), reg); err != nil {
		t.Fatal(err)
	}
	f.a.reloadConfig()
	got = f.wire(t)
	if _, ok := got["max_completion_tokens"]; !ok {
		t.Fatal("model capability reload compared only provider-entry fields")
	}
}

func TestCacheOlderOperatorRowsInheritNewWireFacts(t *testing.T) {
	reg := &providerRegistry{ModelCapabilities: map[string]modelCapability{"gpt-5.6-luna": {Effort: []string{"none"}}}}
	cap, ok := reg.effective().capabilityFor("gpt-5.6-luna")
	if !ok || !cap.modernOutputLimit() || !cap.cacheBreakpoints() {
		t.Fatal("older effort-only override masked required vendor wire facts")
	}
	disabled := false
	reg.ModelCapabilities["gpt-5.6-luna"] = modelCapability{Effort: []string{"none"}, CacheBreakpoints: &disabled, CacheRetentions: []string{}}
	cap, _ = reg.effective().capabilityFor("gpt-5.6-luna")
	if cap.cacheBreakpoints() || cap.CacheRetentions == nil || len(cap.CacheRetentions) != 0 {
		t.Fatal("explicit cache capability override was ignored")
	}
}

func TestCacheCapabilitySnapshotDoesNotAliasReturnedPointers(t *testing.T) {
	reg := newEffectiveCaps(nil)
	cap, _ := reg.capabilityFor("gpt-5.6-luna")
	*cap.CacheBreakpoints = false
	cap.CacheRetentions[0] = "invalid"
	again, _ := reg.capabilityFor("gpt-5.6-luna")
	if !again.cacheBreakpoints() || again.CacheRetentions[0] != "30m" {
		t.Fatal("a returned capability mutated the registry snapshot")
	}
}

func TestModelOutputMaximumCapsResolvedAllocation(t *testing.T) {
	entry := providerEntry{Name: "Claude", MaxOutputTokens: 128000}
	got := limitModelOutput(entry, "claude-haiku-4-5", newEffectiveCaps(nil))
	if got.MaxOutputTokens != 64000 || entry.MaxOutputTokens != 128000 {
		t.Fatalf("resolved maximum or operator source changed: resolved=%d source=%d", got.MaxOutputTokens, entry.MaxOutputTokens)
	}
	entry.MaxOutputTokens = 4096
	if got := limitModelOutput(entry, "claude-haiku-4-5", newEffectiveCaps(nil)); got.MaxOutputTokens != 4096 {
		t.Fatal("vendor maximum enlarged the operator's smaller allocation")
	}
}

func TestCacheReloadRejectsUnsupportedModelRetention(t *testing.T) {
	for _, policy := range []string{"legacy", "typed"} {
		t.Run(policy, func(t *testing.T) {
			f := pass5New(t)
			current := f.a.llmSwap.Current()
			reg, err := loadProvidersFile(f.a.providersPath())
			if err != nil {
				t.Fatal(err)
			}
			if reg.ModelCapabilities == nil {
				reg.ModelCapabilities = map[string]modelCapability{}
			}
			reg.ModelCapabilities["gpt-5.2"] = modelCapability{CacheRetentions: []string{"24h"}}
			if policy == "typed" {
				reg.Providers[0].Cache = &llm.CachePolicy{TTL: "in_memory"}
			}
			if _, err := saveProvidersFile(f.a.providersPath(), reg); err != nil {
				t.Fatal(err)
			}
			_, _, err = f.a.resolveLLM()
			var refusal *llm.CachePolicyError
			if !errors.As(err, &refusal) {
				t.Fatalf("configuration resolution must refuse an unsupported model retention: %v", err)
			}
			f.a.reloadConfig()
			if f.a.llmSwap.Current() != current {
				t.Error("invalid retention edit replaced the working client")
			}
			if got := f.wire(t)["prompt_cache_retention"]; got != "in_memory" {
				t.Fatalf("working policy not retained: %v", got)
			}
		})
	}
}
