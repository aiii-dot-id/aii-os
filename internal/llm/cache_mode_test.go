package llm

import (
	"encoding/json"
	"testing"
)

// Each cache mode is spelled in config as it always was, and marks what it
// names on both dialects that take one: off marks nothing, explicit only
// the stable boundaries, auto the tail as well. The OpenAI explicit-cache
// contract always declares its options, off included, and the legacy extra
// spelling "implicit" reads as auto.
func TestEachCacheModeMarksWhatItNames(t *testing.T) {
	for spelling, mode := range map[string]CacheMode{"auto": CacheAuto, "explicit": CacheExplicit, "off": CacheOff} {
		var p CachePolicy
		if err := json.Unmarshal([]byte(`{"mode":"`+spelling+`"}`), &p); err != nil || p.Mode != mode {
			t.Errorf("config mode %q read as %q (%v), want %q", spelling, p.Mode, err, mode)
		}
	}
	msgs := []Message{{Role: "system", Content: "stable"}, {Role: "user", Content: "go"}}
	tools := ChatOptions{Tools: []ToolDefinition{{Type: "function", Function: ToolFunction{Name: "read", Parameters: map[string]any{"type": "object"}}}}}
	for _, tc := range []struct {
		mode CacheMode
		want int
	}{{CacheOff, 0}, {CacheExplicit, 2}, {CacheAuto, 3}, {"", 3}} {
		got := cacheReviewCapture(t, ClientConfig{Provider: "anthropic", Model: "claude-opus-5", Cache: &CachePolicy{Mode: tc.mode}}, msgs, tools)
		if n := cacheReviewCount(got, "cache_control"); n != tc.want {
			t.Errorf("anthropic mode %q sent %d breakpoints, want %d", tc.mode, n, tc.want)
		}
	}
	for _, tc := range []struct {
		mode        CacheMode
		wire        string
		breakpoints int
	}{{CacheOff, "explicit", 0}, {CacheExplicit, "explicit", 1}, {CacheAuto, "implicit", 1}} {
		got := cacheReviewCapture(t, ClientConfig{Model: "m", ExplicitCache: true, NoStream: true, Cache: &CachePolicy{Mode: tc.mode}}, msgs, ChatOptions{})
		options, _ := got["prompt_cache_options"].(map[string]any)
		if options["mode"] != tc.wire || options["ttl"] != "30m" {
			t.Errorf("openai mode %q declared %v, want mode %q ttl 30m", tc.mode, got["prompt_cache_options"], tc.wire)
		}
		if n := cacheReviewCount(got, "prompt_cache_breakpoint"); n != tc.breakpoints {
			t.Errorf("openai mode %q sent %d breakpoints, want %d", tc.mode, n, tc.breakpoints)
		}
	}
	p, err := ResolveCachePolicy(nil, map[string]any{"prompt_cache_options": map[string]any{"mode": "implicit"}}, DialectOpenAI, true, nil)
	if err != nil || p.Mode != CacheAuto {
		t.Errorf("extra's implicit mode resolved to %q (%v), want %q", p.Mode, err, CacheAuto)
	}
	for _, mode := range []CacheMode{CacheAuto, CacheExplicit, CacheOff} {
		if err := ValidateCachePolicy(CachePolicy{Mode: mode}, DialectAnthropic, false); err != nil {
			t.Errorf("anthropic refused mode %q: %v", mode, err)
		}
	}
}
