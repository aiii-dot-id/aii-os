package llm

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

type pass4Transport func(*http.Request) (*http.Response, error)

func (f pass4Transport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestCacheInconsistentUsageIsNotTrusted(t *testing.T) {
	for _, tc := range []struct{ name, provider, usage string }{
		{"negative OpenAI cache reads", "openai", `{"prompt_tokens":1000,"completion_tokens":10,"total_tokens":1010,"prompt_tokens_details":{"cached_tokens":-500}}`},
		{"OpenAI cache reads exceed input", "openai", `{"prompt_tokens":1000,"completion_tokens":10,"total_tokens":1010,"prompt_tokens_details":{"cached_tokens":2000}}`},
		{"OpenAI total contradicts components", "openai", `{"prompt_tokens":1000,"completion_tokens":10,"total_tokens":0,"prompt_tokens_details":{"cached_tokens":900}}`},
		{"negative Anthropic cache reads", "anthropic", `{"input_tokens":100,"output_tokens":10,"cache_read_input_tokens":-200}`},
		{"negative Anthropic cache writes", "anthropic", `{"input_tokens":100,"output_tokens":10,"cache_creation_input_tokens":-200}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := New(&ClientConfig{Endpoint: "https://review.invalid", Provider: tc.provider, Model: "fixture", Retries: -1})
			c.httpClient = &http.Client{Transport: pass4Transport(func(r *http.Request) (*http.Response, error) {
				body := `{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":` + tc.usage + `}`
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
			})}
			response, err := c.Chat(t.Context(), []Message{{Role: "user", Content: "hello"}}, ChatOptions{})
			if err == nil && response.Usage.Reported {
				t.Fatalf("inconsistent provider usage accepted as a reported measurement: %+v", response.Usage)
			}
		})
	}
}
func TestCacheValidZeroAndCacheTotalsSurvive(t *testing.T) {
	var zero Usage
	if err := json.Unmarshal([]byte(`{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0,"prompt_tokens_details":{"cached_tokens":0}}`), &zero); err != nil {
		t.Fatal(err)
	}
	if !zero.Reported || zero.TotalTokens != 0 {
		t.Fatalf("explicit zero lost: %+v", zero)
	}
	anth := anthUsageOf(&anthUsage{InputTokens: 100, CacheReadInputTokens: 900, CacheCreationInputTokens: 100, OutputTokens: 10})
	if anth.PromptTokens != 1100 || anth.CachedPromptTokens != 900 || anth.TotalTokens != 1110 {
		t.Fatalf("valid Anthropic cache accounting changed: %+v", anth)
	}
	t.Log("explicit zero is reported; valid Anthropic read/write counts still occupy the input window")
}
