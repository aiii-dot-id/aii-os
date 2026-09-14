package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

type cacheReviewTransport func(*http.Request) (*http.Response, error)

func (f cacheReviewTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func cacheReviewCapture(t *testing.T, cfg ClientConfig, msgs []Message, opts ChatOptions) map[string]any {
	t.Helper()
	cfg.Endpoint = "https://cache-review.invalid"
	c := New(&cfg)
	var got map[string]any
	c.httpClient = &http.Client{Transport: cacheReviewTransport(func(r *http.Request) (*http.Response, error) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			return nil, err
		}
		body := `{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"input_tokens":1,"output_tokens":1,"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header), Request: r}, nil
	})}
	if _, err := c.Chat(context.Background(), msgs, opts); err != nil {
		t.Fatal(err)
	}
	return got
}
func cacheReviewCount(v any, key string) int {
	n := 0
	switch x := v.(type) {
	case map[string]any:
		if _, ok := x[key]; ok {
			n++
		}
		for _, v := range x {
			n += cacheReviewCount(v, key)
		}
	case []any:
		for _, v := range x {
			n += cacheReviewCount(v, key)
		}
	}
	return n
}
func TestCacheOpenAIExplicitModeNeedsBreakpoint(t *testing.T) {
	cfg := ClientConfig{Provider: "openai", Model: "gpt-5.6-sol", ExplicitCache: true, Extra: map[string]any{
		"prompt_cache_options": map[string]any{"mode": "explicit", "ttl": "30m"},
		"prompt_cache_key":     "review-stable-key",
	}}
	stable := strings.Repeat("stable context ", 3000)
	got := cacheReviewCapture(t, cfg, []Message{{Role: "system", Content: stable + "\ncurrent work A", StableLen: len(stable)}, {Role: "user", Content: "go"}}, ChatOptions{})
	if got["prompt_cache_key"] != "review-stable-key" {
		t.Fatal("extra did not reach wire")
	}
	if n := cacheReviewCount(got, "prompt_cache_breakpoint"); n == 0 {
		t.Fatalf("explicit mode reached the wire, but %d breakpoints; StableLen=%d was lost", n, len(stable))
	}
}
func TestCacheCachePresenceSurvivesNormalization(t *testing.T) {
	var absent, zero Usage
	if err := json.Unmarshal([]byte(`{"prompt_tokens":1000,"completion_tokens":10,"total_tokens":1010}`), &absent); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(`{"prompt_tokens":1000,"completion_tokens":10,"total_tokens":1010,"prompt_tokens_details":{"cached_tokens":0}}`), &zero); err != nil {
		t.Fatal(err)
	}
	if reflect.DeepEqual(absent, zero) {
		t.Fatalf("unreported cache usage and a reported zero become identical: %+v", zero)
	}
}
func TestCacheCacheWritesSurviveNormalization(t *testing.T) {
	t.Run("Anthropic", func(t *testing.T) {
		ordinary := anthUsageOf(&anthUsage{InputTokens: 1000, OutputTokens: 10})
		write := anthUsageOf(&anthUsage{CacheCreationInputTokens: 1000, OutputTokens: 10})
		if reflect.DeepEqual(ordinary, write) {
			t.Fatalf("1000 ordinary input tokens and 1000 cache-write tokens become identical: %+v", write)
		}
	})
	t.Run("OpenAI", func(t *testing.T) {
		var ordinary, write Usage
		if err := json.Unmarshal([]byte(`{"prompt_tokens":1000,"completion_tokens":10,"total_tokens":1010,"prompt_tokens_details":{"cached_tokens":0,"cache_write_tokens":0}}`), &ordinary); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal([]byte(`{"prompt_tokens":1000,"completion_tokens":10,"total_tokens":1010,"prompt_tokens_details":{"cached_tokens":0,"cache_write_tokens":1000}}`), &write); err != nil {
			t.Fatal(err)
		}
		if reflect.DeepEqual(ordinary, write) {
			t.Fatalf("1000 ordinary input tokens and 1000 cache-write tokens become identical: %+v", write)
		}
	})
}
func TestCacheChatGPTReportsCacheHits(t *testing.T) {
	s := "data: " + `{"type":"response.completed","response":{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":1000,"output_tokens":10,"total_tokens":1010,"input_tokens_details":{"cached_tokens":800}}}}` + "\n\n"
	resp, err := readResponsesStream(strings.NewReader(s))
	if err != nil {
		t.Fatal(err)
	}
	if resp.Usage.CachedPromptTokens != 800 {
		t.Fatalf("response reported 800 cached tokens, normalized usage: %+v", resp.Usage)
	}
}
func TestCacheAnthropicBreakpointLimit(t *testing.T) {
	msgs := []Message{}
	for i := 0; i < 4; i++ {
		msgs = append(msgs, Message{Role: "system", Content: "system instruction"})
	}
	msgs = append(msgs, Message{Role: "user", Content: "go"})
	got := cacheReviewCapture(t, ClientConfig{Provider: "anthropic", Model: "claude-opus-5"}, msgs, ChatOptions{Tools: []ToolDefinition{{Type: "function", Function: ToolFunction{Name: "inspect"}}}})
	if n := cacheReviewCount(got, "cache_control"); n > 4 {
		t.Fatalf("sent %d cache breakpoints for four system messages + one tool + user tail; maximum 4", n)
	}
}
func TestCacheAnthropicConfiguredTTLReachesWire(t *testing.T) {
	got := cacheReviewCapture(t, ClientConfig{Provider: "anthropic", Model: "claude-opus-5", Cache: &CachePolicy{TTL: "1h"}}, []Message{{Role: "system", Content: "stable"}, {Role: "user", Content: "go"}}, ChatOptions{})
	if n := cacheReviewCount(got, "ttl"); n == 0 {
		t.Fatalf("configured 1h TTL did not reach wire; all %d cache markers use vendor default", cacheReviewCount(got, "cache_control"))
	}
}
