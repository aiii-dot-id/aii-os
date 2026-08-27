package app

import (
	"context"
	"sync"

	"github.com/aiii-dot-id/aii-os/internal/llm"
)

// swappableLLM lets the operator change provider settings (key, endpoint,
// model, budgets) from the dashboard WITHOUT restarting the identity: the
// conversation loop and every facility hold THIS adapter; a config change
// swaps the underlying client atomically. The identity's continuity does
// not depend on its provider socket.
type swappableLLM struct {
	mu     sync.RWMutex
	client *llm.Client
}

func newSwappableLLM(c *llm.Client) *swappableLLM {
	return &swappableLLM{client: c}
}

// Swap replaces the underlying client. In-flight calls finish on the old
// client; subsequent calls use the new one.
func (s *swappableLLM) Swap(c *llm.Client) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.client = c
}

// Current exposes the active client (config application + tests).
func (s *swappableLLM) Current() *llm.Client {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.client
}

// Chat implements conversation.LLMClient.
func (s *swappableLLM) Chat(ctx context.Context, messages []llm.Message, opts llm.ChatOptions) (*llm.Response, error) {
	return s.Current().Chat(ctx, messages, opts)
}

// ChatStructured implements cognitive.LLMCaller.
func (s *swappableLLM) ChatStructured(ctx context.Context, systemPrompt, userMessage string, tool llm.ToolDefinition) (string, string, bool, error) {
	return s.Current().ChatStructured(ctx, systemPrompt, userMessage, tool)
}

// ChatSimple implements cognitive.LLMCaller.
func (s *swappableLLM) ChatSimple(ctx context.Context, systemPrompt, userMessage string) (string, string, error) {
	return s.Current().ChatSimple(ctx, systemPrompt, userMessage)
}
