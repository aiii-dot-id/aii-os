package conversation

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/llm"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func pass4WireResponse(provider, stop, content string, tool bool, n int) map[string]any {
	usage := map[string]any{"prompt_tokens": 1000, "completion_tokens": 10, "total_tokens": 1010, "prompt_tokens_details": map[string]any{"cached_tokens": 900}}
	if provider == "anthropic" {
		usage = map[string]any{"input_tokens": 100, "cache_read_input_tokens": 900, "output_tokens": 10}
		block := map[string]any{"type": "text", "text": content}
		if tool {
			block = map[string]any{"type": "tool_use", "id": fmt.Sprintf("c%d", n), "name": "read", "input": map[string]any{}}
			stop = "tool_use"
		}
		return map[string]any{"content": []any{block}, "stop_reason": stop, "usage": usage}
	}
	msg := map[string]any{"role": "assistant", "content": content}
	if tool {
		msg["tool_calls"] = []any{map[string]any{"id": fmt.Sprintf("c%d", n), "type": "function", "function": map[string]any{"name": "read", "arguments": "{}"}}}
		stop = "tool_calls"
	}
	return map[string]any{"choices": []any{map[string]any{"message": msg, "finish_reason": stop}}, "usage": usage}
}
func TestCacheBadTotalCannotBypassTokenFence(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Tools      []json.RawMessage `json:"tools"`
			ToolChoice string            `json:"tool_choice"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			return
		}
		n := int(requests.Add(1))
		tool := len(req.Tools) > 0 && req.ToolChoice != "none"
		response := pass4WireResponse("openai", "stop", "done", tool, n)
		if tool {
			response["usage"].(map[string]any)["total_tokens"] = 0
		}
		if err := json.NewEncoder(w).Encode(response); err != nil {
			t.Error(err)
		}
	}))
	t.Cleanup(srv.Close)
	client := llm.New(&llm.ClientConfig{Endpoint: srv.URL, Provider: "openai", Model: "fixture", Retries: -1, TimeoutSeconds: 2})
	ft := &fakeTools{results: map[string]string{"read": "ok"}}
	defs := &fakeDefs{defs: []llm.ToolDefinition{{Type: "function", Function: llm.ToolFunction{Name: "read"}}}}
	loop := New(client, ft, defs, nil, nil, Config{MaxIterations: 4, ContextBudgetTokens: 100000, TurnTokenBudget: 1500})
	result, err := loop.Run(t.Context(), "stable", []llm.Message{{Role: "user", Content: "go"}})
	if err != nil {
		var usageErr *llm.UsageError
		if !errors.As(err, &usageErr) {
			t.Fatal(err)
		}
		return
	}
	if len(ft.calls) > 2 {
		t.Fatalf("1500-token fence allowed %d tool rounds; reported input=%d output=%d cached=%d but total=%d; provider totals contradicted their components", len(ft.calls), result.Usage.PromptTokens, result.Usage.CompletionTokens, result.Usage.CachedPromptTokens, result.Usage.TotalTokens)
	}
}
func TestCacheWrapUpDisclosesProviderStop(t *testing.T) {
	for _, tc := range []struct{ provider, stop, expected string }{
		{"openai", "length", "cut off"},
		{"anthropic", "max_tokens", "cut off"},
		{"anthropic", "model_context_window_exceeded", "model_context_window_exceeded"},
	} {
		t.Run(tc.provider+"/"+tc.stop, func(t *testing.T) {
			var requests atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				n := int(requests.Add(1))
				if err := json.NewEncoder(w).Encode(pass4WireResponse(tc.provider, tc.stop, "partial answer", n == 1, n)); err != nil {
					t.Error(err)
				}
			}))
			t.Cleanup(srv.Close)
			c := llm.New(&llm.ClientConfig{Endpoint: srv.URL, Provider: tc.provider, Model: "fixture", Retries: -1, TimeoutSeconds: 2})
			loop := New(c, &fakeTools{results: map[string]string{"read": "ok"}}, &fakeDefs{defs: []llm.ToolDefinition{{Type: "function", Function: llm.ToolFunction{Name: "read"}}}}, nil, nil, Config{MaxIterations: 1, ContextBudgetTokens: 100000})
			result, err := loop.Run(t.Context(), "stable", []llm.Message{{Role: "user", Content: "go"}})
			if err != nil {
				return
			}
			if requests.Load() != 2 || result.Usage.CachedPromptTokens != 1800 {
				t.Fatalf("fixture did not run two cached requests: n=%d usage=%+v", requests.Load(), result.Usage)
			}
			if !strings.Contains(result.Spoken, tc.expected) {
				t.Fatalf("provider stop %q disappeared from wrap-up; result=%q", tc.stop, result.Spoken)
			}
		})
	}
}
func TestCacheOrdinaryFinalReplyStaysClean(t *testing.T) {
	r := textResp("done")
	r.Choices[0].FinishReason = "stop"
	result, model, err := finalResponse(&r, "fixture")
	if err != nil || result != "done" || model != "" {
		t.Fatalf("ordinary final response changed: %q %q %v", result, model, err)
	}
}
