package conversation

import (
	"bytes"
	"context"
	"github.com/aiii-dot-id/aii-os/internal/llm"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestCacheSilentFailurePreservesReportedUsage(t *testing.T) {
	first := resp("", toolCall("c1", "read", "{}"))
	first.Usage = llm.Usage{PromptTokens: 1000, CompletionTokens: 10, TotalTokens: 1010, CachedPromptTokens: 900, Reported: true}
	client := &scriptLLM{script: []llm.Response{first}}
	loop := New(client, &fakeTools{results: map[string]string{"read": "ok"}}, &fakeDefs{}, nil, nil, Config{MaxIterations: 3, ContextBudgetTokens: 100000})
	result, err := loop.Run(t.Context(), "stable", []llm.Message{{Role: "user", Content: "inspect"}})
	if err == nil || len(client.requests) != 2 {
		t.Fatalf("fixture expected a successful tool call then script exhaustion; err=%v requests=%d", err, len(client.requests))
	}
	if result.Usage.Calls != 2 || result.Usage.Silent != 1 || result.Usage.TotalTokens != 1010 || result.Usage.CachedPromptTokens != 900 {
		t.Fatalf("known 1010 tokens including 900 cached were lost on failure before speech; returned usage=%+v", result.Usage)
	}
}
func TestCacheRetryDoesNotClaimCompleteUsage(t *testing.T) {
	for _, provider := range []string{"openai", "anthropic"} {
		t.Run(provider, func(t *testing.T) {
			var requests atomic.Int32
			bodies := make(chan []byte, 3)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				b, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
					return
				}
				bodies <- b
				if requests.Add(1) == 1 {
					conn, _, err := w.(http.Hijacker).Hijack()
					if err != nil {
						t.Error(err)
						return
					}
					conn.Close()
					return
				}
				if provider == "anthropic" {
					io.WriteString(w, `{"content":[{"type":"text","text":"done"}],"stop_reason":"end_turn","usage":{"input_tokens":100,"cache_read_input_tokens":900,"output_tokens":10}}`)
				} else {
					io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"done"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1000,"completion_tokens":10,"total_tokens":1010,"prompt_tokens_details":{"cached_tokens":900}}}`)
				}
			}))
			t.Cleanup(server.Close)
			c := llm.New(&llm.ClientConfig{Endpoint: server.URL, Provider: provider, Model: "fixture", Retries: 1, RetryBackoffMS: 1, TimeoutSeconds: 2})
			loop := New(c, &fakeTools{}, &fakeDefs{}, nil, nil, Config{MaxIterations: 3, ContextBudgetTokens: 100000})
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			result, err := loop.Run(ctx, "stable identity", []llm.Message{{Role: "user", Content: "hello"}})
			if err != nil {
				t.Fatal(err)
			}
			if requests.Load() != 2 {
				t.Fatalf("fixture expected two server-received requests; got %d", requests.Load())
			}
			first, second := <-bodies, <-bodies
			if !bytes.Equal(first, second) {
				t.Fatal("retry changed the request body")
			}
			t.Log("both POST bodies were identical; first request was read fully before its connection was closed without any usage response")
			if result.Usage.Complete() {
				t.Fatalf("two dispatched requests, first attempt usage unknown, but final usage claims complete: %+v", result.Usage)
			}
		})
	}
}
