package app

import (
	"context"
	"sync"

	"github.com/aiii-dot-id/aii-os/internal/llm"
)

// .
// .
// .
// .
// .
type swappableLLM struct {
	mu     sync.RWMutex
	client *llm.Client
}

func newSwappableLLM(c *llm.Client) *swappableLLM {
	return &swappableLLM{client: c}
}

// .
// .
func (s *swappableLLM) Swap(c *llm.Client) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.client = c
}

// .
func (s *swappableLLM) Current() *llm.Client {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.client
}

// .
func (s *swappableLLM) Chat(ctx context.Context, messages []llm.Message, opts llm.ChatOptions) (*llm.Response, error) {
	return s.Current().Chat(ctx, messages, opts)
}

// .
func (s *swappableLLM) ChatStructured(ctx context.Context, systemPrompt, userMessage string, tool llm.ToolDefinition) (string, string, bool, error) {
	return s.Current().ChatStructured(ctx, systemPrompt, userMessage, tool)
}

// .
// .
// .
func (s *swappableLLM) CheckSimple(ctx context.Context, systemPrompt, userMessage string) error {
	return s.Current().CheckSimple(ctx, systemPrompt, userMessage)
}

// .
func (s *swappableLLM) ChatSimple(ctx context.Context, systemPrompt, userMessage string) (string, string, error) {
	return s.Current().ChatSimple(ctx, systemPrompt, userMessage)
}
