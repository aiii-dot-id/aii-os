package llm

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestClientRefusesOversizeInputBeforeDispatch(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		calls.Add(1)
	}))
	t.Cleanup(server.Close)

	client := New(&ClientConfig{Endpoint: server.URL, Model: "test", MaxInputTokens: 10})
	_, err := client.Chat(context.Background(), []Message{{Role: "user", Content: strings.Repeat("x", 200)}}, ChatOptions{})
	var limitErr *ContextLimitError
	if !errors.As(err, &limitErr) {
		t.Fatalf("want typed context refusal, got %v", err)
	}
	if calls.Load() != 0 {
		t.Fatal("oversize request reached the provider")
	}
}

func TestInputEstimateIncludesStructuredCallsAndTools(t *testing.T) {
	plain, err := EstimateInputTokens([]Message{{Role: "user", Content: "go"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	call := ToolCall{ID: "call", Type: "function"}
	call.Function.Name = "search"
	call.Function.Arguments = strings.Repeat("x", 1000)
	tool := ToolDefinition{Type: "function", Function: ToolFunction{
		Name: "search", Description: strings.Repeat("schema", 100),
		Parameters: map[string]any{"type": "object"},
	}}
	full, err := EstimateInputTokens([]Message{{Role: "assistant", ToolCalls: []ToolCall{call}}}, []ToolDefinition{tool})
	if err != nil {
		t.Fatal(err)
	}
	if full <= plain {
		t.Fatalf("structured input was not counted: full=%d plain=%d", full, plain)
	}
}

func TestClaudeOAuthBillingBlockCountsTowardAdmission(t *testing.T) {
	messages := []Message{{Role: "user", Content: "hi"}}
	without, err := EstimateInputTokens(messages, nil)
	if err != nil {
		t.Fatal(err)
	}
	client := New(&ClientConfig{
		Endpoint: "http://unused.invalid", Model: "claude-opus-5", Provider: "anthropic",
		Credential: fixedCredential{token: "token"}, AnthropicOAuthBillingText: strings.Repeat("billing ", 100),
		MaxInputTokens: without,
	})
	_, err = client.Chat(context.Background(), messages, ChatOptions{})
	var limitErr *ContextLimitError
	if !errors.As(err, &limitErr) {
		t.Fatalf("billing block must be admitted with the request, got %v", err)
	}
}

func TestTheAdmissionEstimateKeepsItsBytes(t *testing.T) {
	call := ToolCall{ID: "call_1", Type: "function", EmissionOrdinal: 3}
	call.Function.Name = "recall"
	call.Function.Arguments = `{"q":"tides"}`
	tool := ToolDefinition{Type: "function", Function: ToolFunction{Name: "recall", Description: "find", Parameters: map[string]any{"type": "object"}}}
	got, err := EstimateInputTokens([]Message{
		{Role: "system", Content: "you are", StableLen: 3},
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi <b>&", ToolCallID: "c0", ToolCalls: []ToolCall{call}, StableLen: 2, CacheBefore: true, Abridged: true, Qualifies: true,
			Reasoning: "r", Thinking: []ThinkingBlock{{Kind: "thinking", Text: "t", Signature: "s"}},
			NativeContent: json.RawMessage(`[1]`), NativeDialect: "anthropic", OutputTokens: 9},
		{Role: "assistant", Content: "y", ToolCalls: []ToolCall{call},
			Thinking: []ThinkingBlock{{Kind: "thinking", Text: "plan", Signature: "sig"}, {Kind: "redacted_thinking", Data: "opaque"}}},
		{Role: "assistant", Content: "x", Reasoning: "because"},
		{Role: "assistant", NativeContent: json.RawMessage(`[{"type":"text","text":"native"}]`)},
		{Role: "tool", ToolCallID: "call_1", Content: "result"},
	}, []ToolDefinition{tool})
	if err != nil || got != 243 {
		t.Fatalf("the estimate of a fixed request is %d (%v), want 243", got, err)
	}
}
