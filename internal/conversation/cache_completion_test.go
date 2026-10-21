package conversation

import (
	"encoding/json"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/limits"
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
	for name, tc := range map[string]struct {
		ask    string
		rounds int
		fenced bool
	}{
		"a request the estimate prices over the fence": {strings.Repeat("one line of what was asked\n", 300), 1, true},
		"a request the estimate prices well under it":  {"go", 3, false},
	} {
		t.Run(name, func(t *testing.T) {
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
			loop := New(client, ft, defs, nil, nil, testBounds(Config{MaxIterations: limits.Of(3), ContextBudgetTokens: 100000, TurnTokenBudget: limits.Of(1500)}))
			result, err := loop.Run(t.Context(), "stable", []llm.Message{{Role: "user", Content: tc.ask}})
			if err != nil {
				t.Fatalf("a turn was refused for its provider's token counts: %v", err)
			}
			if len(ft.calls) != tc.rounds || result.ContinuedAtTokenBudget != tc.fenced || strings.Contains(result.Spoken, "token budget (1500) is spent") != tc.fenced {
				t.Fatalf("the 1500-token fence, charged by the estimate: %d tool round(s), fenced=%t, want %d and %t; spoken %q",
					len(ft.calls), result.ContinuedAtTokenBudget, tc.rounds, tc.fenced, result.Spoken)
			}
			if !strings.Contains(result.Spoken, "done") {
				t.Fatalf("the replies were not kept: %q", result.Spoken)
			}
			if u := result.Usage; u.Calls != tc.rounds+1 || u.Silent != tc.rounds || u.TotalTokens != 1010 || u.PromptTokens != 1000 || u.CompletionTokens != 10 || u.CachedPromptTokens != 900 || u.Complete() {
				t.Fatalf("the turn's usage holds counts that were not a measurement, or calls them complete: %+v", u)
			}
		})
	}
	// "Not reported" has one meaning. Usage refused as inconsistent folds as
	// usage a provider never sent, a facility's call included (OneCall), and
	// a call's log line says so of both, where a reported zero is a count.
	refused, never := llm.Usage{Problem: "invalid_usage", Refused: "input=1000 output=10 total=0"}, llm.Usage{}
	if got := OneCall(refused); got != OneCall(never) || got.Calls != 1 || got.Silent != 1 || got.TotalTokens != 0 {
		t.Fatalf("usage refused as inconsistent folds as %+v, and usage never sent as %+v", got, OneCall(never))
	}
	if a, b, zero := reportedTotal(refused), reportedTotal(never), reportedTotal(llm.Usage{Reported: true}); a != "not reported" || b != "not reported" || zero != "0" {
		t.Fatalf("a call's total reads %q refused, %q never sent, %q reported as zero", a, b, zero)
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
			loop := New(c, &fakeTools{results: map[string]string{"read": "ok"}}, &fakeDefs{defs: []llm.ToolDefinition{{Type: "function", Function: llm.ToolFunction{Name: "read"}}}}, nil, nil, testBounds(Config{MaxIterations: limits.Of(1), ContextBudgetTokens: 100000}))
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

// A reply the output limit cut inside its only tool call, with no text
// before it, reaches the loop with neither text nor a call: the client
// leaves the unfinished call out. That is the limit's doing and is said in
// the sentence for a cut reply; it is not the substrate failing mid-turn,
// and the call is never run. The same emptiness under an ordinary stop is
// still no answer and fails the turn.
func TestAReplyTheOutputLimitCutInsideItsOnlyCallEndsAsTheDeclaredCut(t *testing.T) {
	const declared = "[This reply was cut off by the model's output limit. Raise the model's max output tokens to see the rest.]"
	for name, delta := range map[string]string{
		"half its arguments": `{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"path\":\"a.txt\",\"content\":\"half of the fi"}}`,
		"no argument byte":   "",
	} {
		t.Run(name, func(t *testing.T) {
			var requests atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				w.Header().Set("Content-Type", "text/event-stream")
				for _, frame := range []string{
					`{"type":"message_start","message":{"id":"m","content":[],"usage":{"input_tokens":11,"output_tokens":0}}}`,
					`{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"call_1","name":"write","input":{}}}`,
					delta,
					`{"type":"content_block_stop","index":0}`,
					`{"type":"message_delta","delta":{"stop_reason":"max_tokens"},"usage":{"output_tokens":9}}`,
					`{"type":"message_stop"}`,
				} {
					if frame != "" {
						fmt.Fprintf(w, "data: %s\n\n", frame)
					}
				}
			}))
			t.Cleanup(srv.Close)
			c := llm.New(&llm.ClientConfig{Endpoint: srv.URL, APIKey: "k", Provider: "anthropic", Model: "fixture", Retries: -1, TimeoutSeconds: 2})
			ft := &fakeTools{results: map[string]string{"write": "ok"}}
			defs := &fakeDefs{defs: []llm.ToolDefinition{{Type: "function", Function: llm.ToolFunction{Name: "write"}}}}
			loop := New(c, ft, defs, nil, nil, testBounds(Config{MaxIterations: limits.Of(4), ContextBudgetTokens: 100000}))
			result, err := loop.Run(t.Context(), "stable", []llm.Message{{Role: "user", Content: "write the file"}})
			if err != nil {
				t.Fatalf("a reply cut inside its only call failed the turn: %v (the identity is told %q)", err, result.Interrupted)
			}
			if result.Spoken != declared || result.Interrupted != "" {
				t.Fatalf("spoken %q interrupted %q, want only the declared cut", result.Spoken, result.Interrupted)
			}
			if len(ft.calls) != 0 || requests.Load() != 1 {
				t.Fatalf("the cut call ran or the turn went on: calls=%v requests=%d", ft.calls, requests.Load())
			}
			if result.Usage.PromptTokens != 11 || result.Usage.CompletionTokens != 9 {
				t.Fatalf("the cut reply's usage was lost: %+v", result.Usage)
			}
		})
	}

	empty := &scriptLLM{script: []llm.Response{textResp("")}}
	if _, err := New(empty, &fakeTools{}, &fakeDefs{}, nil, nil, testBounds(Config{ContextBudgetTokens: 1000})).Run(t.Context(), "system", []llm.Message{{Role: "user", Content: "go"}}); err == nil {
		t.Fatal("a reply with no text, no call and no cut was taken for an answer")
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
