package app

import (
	"context"
	"sync"

	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/llm/wire"
)

type swappableLLM struct {
	mu     sync.RWMutex
	client *llm.Client
}

func newSwappableLLM(c *llm.Client) *swappableLLM {
	return &swappableLLM{client: c}
}

func (s *swappableLLM) Swap(c *llm.Client) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.client = c
}

func (s *swappableLLM) Current() *llm.Client {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.client
}

func (s *swappableLLM) Chat(ctx context.Context, messages []llm.Message, opts llm.ChatOptions) (*llm.Response, error) {
	return s.Current().Chat(ctx, messages, opts)
}

func (s *swappableLLM) CheckSimple(ctx context.Context, systemPrompt, userMessage string) error {
	return s.Current().CheckSimple(ctx, systemPrompt, userMessage)
}

type facilityLLM struct {
	*swappableLLM
	a *App
}

func (f facilityLLM) ChatSimple(ctx context.Context, systemPrompt, userMessage string) (string, string, error) {
	c := f.Current()
	text, modelID, usage, err := c.ChatSimple(ctx, systemPrompt, userMessage)
	f.a.recordFacilityCall(wire.TapSource(ctx), c.ModelName(), usage)
	return text, modelID, err
}

func (f facilityLLM) ChatStructured(ctx context.Context, systemPrompt, userMessage string, tool llm.ToolDefinition) (string, string, bool, error) {
	c := f.Current()
	payload, modelID, viaTool, usage, err := c.ChatStructured(ctx, systemPrompt, userMessage, tool)
	f.a.recordFacilityCall(wire.TapSource(ctx), c.ModelName(), usage)
	return payload, modelID, viaTool, err
}

func (f facilityLLM) Chat(ctx context.Context, messages []llm.Message, opts llm.ChatOptions) (*llm.Response, error) {
	c := f.Current()
	resp, err := c.Chat(ctx, messages, opts)
	f.a.recordFacilityCall(wire.TapSource(wire.WithTapSource(ctx, opts.Source)), c.ModelName(), llm.CallUsage(resp, err))
	return resp, err
}
