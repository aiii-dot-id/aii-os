package conversation

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/llm"
)

type reviewerRoundTrip func(*http.Request) (*http.Response, error)

func (f reviewerRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func reviewerReply(r *http.Request, v any) (*http.Response, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(b))), Request: r}, nil
}

// .
// .
func TestReviewerRetryFenceIncludesToolSchemas(t *testing.T) {
	defs := &fakeDefs{defs: []llm.ToolDefinition{{Type: "function", Function: llm.ToolFunction{
		Name: "read", Description: strings.Repeat("documented tool schema field ", 3000),
		Parameters: map[string]interface{}{"type": "object"},
	}}}}
	msgs := []llm.Message{{Role: "system", Content: "stable" + systemAdditions()}, {Role: "user", Content: "go"}}
	input, err := llm.EstimateInputTokens(msgs, defs.defs)
	if err != nil {
		t.Fatal(err)
	}
	budget := input * 3 / 2
	calls := 0
	var choices []string
	var firstBody []byte
	saved := http.DefaultTransport
	http.DefaultTransport = reviewerRoundTrip(func(r *http.Request) (*http.Response, error) {
		b, err := io.ReadAll(r.Body)
		if err != nil {
			return nil, err
		}
		calls++
		var req llm.ChatRequest
		if err := json.Unmarshal(b, &req); err != nil {
			return nil, err
		}
		if calls == 1 {
			firstBody = b
			return nil, errors.New("synthetic connection loss after full request consumption")
		}
		if calls == 2 && string(firstBody) != string(b) {
			t.Error("retry did not preserve body")
		}
		choices = append(choices, req.ToolChoice)
		n, err := llm.EstimateInputTokens(req.Messages, req.Tools)
		if err != nil {
			return nil, err
		}
		response := pass4WireResponse("openai", "stop", "done", req.ToolChoice != "none" && calls < 4, calls)
		response["usage"] = map[string]any{"prompt_tokens": n, "completion_tokens": 10, "total_tokens": n + 10}
		return reviewerReply(r, response)
	})
	t.Cleanup(func() { http.DefaultTransport = saved })
	client := llm.New(&llm.ClientConfig{Endpoint: "http://review.invalid", Provider: "openai", Model: "synthetic", Retries: 1, RetryBackoffMS: 1})
	tools := &fakeTools{results: map[string]string{"read": "ok"}}
	loop := New(client, tools, defs, nil, nil, Config{MaxIterations: 5, TurnTokenBudget: budget, ContextBudgetTokens: 1_000_000})
	result, err := loop.Run(t.Context(), "stable", []llm.Message{{Role: "user", Content: "go"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("input including schema=%d silent estimate=%d budget=%d physical attempts=%d choices=%q tool executions=%d usage=%+v", input, estimateSilentCall(msgs, defs.defs, "done"), budget, calls, choices, len(tools.calls), result.Usage)
	if result.Usage.UnknownAttempts != 1 {
		t.Fatal("fixture did not produce exactly one unknown attempt")
	}
	if len(choices) < 2 || choices[1] != "none" || len(tools.calls) != 1 {
		t.Fatalf("known first success (%d) + conservative unknown request estimate (%d) exceeds fence %d, but next call still enabled tools", input+10, input, budget)
	}
}

func TestReviewerNoChoicesPreservesKnownUsage(t *testing.T) {
	saved := http.DefaultTransport
	http.DefaultTransport = reviewerRoundTrip(func(r *http.Request) (*http.Response, error) {
		return reviewerReply(r, map[string]any{"choices": []any{}, "usage": map[string]any{
			"prompt_tokens": 1000, "completion_tokens": 10, "total_tokens": 1010,
			"prompt_tokens_details": map[string]any{"cached_tokens": 900},
		}})
	})
	t.Cleanup(func() { http.DefaultTransport = saved })
	client := llm.New(&llm.ClientConfig{Endpoint: "http://review.invalid", Provider: "openai", Model: "synthetic", Retries: -1})
	loop := New(client, &fakeTools{}, &fakeDefs{}, nil, nil, Config{MaxIterations: 2, ContextBudgetTokens: 100000})
	result, err := loop.Run(t.Context(), "stable", []llm.Message{{Role: "user", Content: "go"}})
	if err == nil {
		t.Fatal("expected no-choices refusal")
	}
	t.Logf("err=%v; usage=%+v", err, result.Usage)
	if result.Usage.TotalTokens != 1010 || result.Usage.CachedPromptTokens != 900 || result.Usage.Silent != 0 {
		t.Fatalf("refusing content lost known usage: %+v", result.Usage)
	}
}
