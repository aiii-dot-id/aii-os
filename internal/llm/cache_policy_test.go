package llm

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

func TestCachePolicyRefusesUnsupportedCombinations(t *testing.T) {
	for _, tc := range []struct {
		policy   CachePolicy
		dialect  Dialect
		explicit bool
	}{
		{CachePolicy{TTL: "24h"}, DialectAnthropic, false},
		{CachePolicy{TTL: "5m", TailTTL: "1h"}, DialectAnthropic, false},
		{CachePolicy{Mode: "off"}, DialectOpenAI, false},
		{CachePolicy{TTL: "in_memory"}, DialectOpenAI, true},
		{CachePolicy{TTL: "30m"}, DialectResponses, false},
	} {
		var e *CachePolicyError
		if !errors.As(ValidateCachePolicy(tc.policy, tc.dialect, tc.explicit), &e) {
			t.Errorf("unsupported cache policy accepted: %+v", tc)
		}
	}
}
func TestCacheExplicitClearOverridesLegacyExtra(t *testing.T) {
	c := New(&ClientConfig{Cache: &CachePolicy{}, Extra: map[string]any{"prompt_cache_key": "old", "prompt_cache_retention": "24h"}})
	body, err := c.mergeExtra([]byte(`{"messages":[{"role":"user","content":"hi"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	body, err = c.applyOpenAICache(body, []Message{{Role: "user", Content: "hi"}})
	if err != nil {
		t.Fatal(err)
	}
	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"prompt_cache_key", "prompt_cache_retention"} {
		if _, ok := req[key]; ok {
			t.Fatalf("cleared %s returned from legacy extra", key)
		}
	}
}
func TestCacheDiagnosticStatesAndCorrelation(t *testing.T) {
	cases := []struct{ raw, previous, want string }{{"", "", "not_returned"}, {"null", "", "no_comparison"}, {"null", "prior", "no_divergence"}, {`{"cache_miss_reason":null}`, "prior", "pending"}, {`{"cache_miss_reason":{"type":"system_changed"}}`, "prior", "system_changed"}}
	for _, tc := range cases {
		if got := cacheDiagnosticState(json.RawMessage(tc.raw), tc.previous); got != tc.want {
			t.Errorf("%s => %s, want %s", tc.raw, got, tc.want)
		}
	}
}
func TestResetThinkingKeepsOtherNativeBlocks(t *testing.T) {
	messages := []Message{{Role: "assistant", NativeDialect: "anthropic", NativeContent: json.RawMessage(`[{"type":"text","text":"before"},{"type":"thinking","thinking":"","signature":"opaque"},{"type":"vendor_extension","data":{"value":42}},{"type":"tool_use","id":"c","name":"read","input":{}}]`), OutputTokens: 1000}}
	if n := ResetThinking(messages); n != 1 {
		t.Fatalf("reset count=%d", n)
	}
	var got []map[string]any
	if err := json.Unmarshal(messages[0].NativeContent, &got); err != nil {
		t.Fatal(err)
	}
	kinds := []string{}
	for _, b := range got {
		kinds = append(kinds, b["type"].(string))
	}
	if !reflect.DeepEqual(kinds, []string{"text", "vendor_extension", "tool_use"}) {
		t.Fatalf("retained block sequence changed: %v", kinds)
	}
	before := append([]byte(nil), messages[0].NativeContent...)
	if n := ResetThinking(messages); n != 0 || string(before) != string(messages[0].NativeContent) {
		t.Fatal("an already-reset message was rewritten")
	}
}

func TestModernOutputCeilingUsesDeclaredField(t *testing.T) {
	got := cacheReviewCapture(t, ClientConfig{MaxCompletionTokens: true, MaxOutputTokens: 4096}, []Message{{Role: "user", Content: "hello"}}, ChatOptions{})
	if got["max_completion_tokens"] != float64(4096) {
		t.Fatalf("wrong output ceiling: %v", got["max_completion_tokens"])
	}
	if _, exists := got["max_tokens"]; exists {
		t.Fatal("legacy ceiling sent beside modern ceiling")
	}
}

func TestCacheModelPolicyIsImmutableAndExplicitEmptyIsRefused(t *testing.T) {
	policy := &CachePolicy{TTL: "24h"}
	retention := []string{"24h"}
	c := New(&ClientConfig{Cache: policy, CacheRetentions: retention})
	policy.TTL = "in_memory"
	retention[0] = "in_memory"
	got, err := c.cachePolicy()
	if err != nil || got.TTL != "24h" {
		t.Fatalf("client's policy changed through its constructor input: %+v %v", got, err)
	}
	c = New(&ClientConfig{Cache: &CachePolicy{TTL: "24h"}, CacheRetentions: []string{}})
	if _, err := c.cachePolicy(); err == nil {
		t.Fatal("explicitly empty model retention capabilities became unknown")
	}
}
