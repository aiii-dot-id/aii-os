package llm

import (
	"context"
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
		Parameters: map[string]interface{}{"type": "object"},
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
