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

func (s *swappableLLM) WithheldTools(defs []llm.ToolDefinition) ([]llm.ToolDefinition, []string) {
	return s.Current().WithheldTools(defs)
}

func (s *swappableLLM) CheckSimple(ctx context.Context, systemPrompt, userMessage string) error {
	return s.Current().CheckSimple(ctx, systemPrompt, userMessage)
}

func (s *swappableLLM) CheckWithTools(ctx context.Context, systemPrompt, userMessage string, tools []llm.ToolDefinition) error {
	return s.Current().CheckWithTools(ctx, systemPrompt, userMessage, tools)
}

type facilityLLM struct {
	*swappableLLM
	a *App
}

func (f facilityLLM) within(ctx context.Context) context.Context {
	budget, _ := f.a.currentFacilityBudget()
	return wire.WithInputCeiling(ctx, budget)
}

func (f facilityLLM) CheckSimple(ctx context.Context, systemPrompt, userMessage string) error {
	return f.swappableLLM.CheckSimple(f.within(ctx), systemPrompt, userMessage)
}

func (f facilityLLM) CheckWithTools(ctx context.Context, systemPrompt, userMessage string, tools []llm.ToolDefinition) error {
	return f.swappableLLM.CheckWithTools(f.within(ctx), systemPrompt, userMessage, tools)
}

func (f facilityLLM) ChatSimple(ctx context.Context, systemPrompt, userMessage string) (string, string, error) {
	ctx = f.within(ctx)
	c := f.Current()
	text, modelID, usage, err := c.ChatSimple(ctx, systemPrompt, userMessage)
	f.a.recordFacilityCall(wire.TapSource(ctx), c.ModelName(), usage)
	return text, modelID, err
}

func (f facilityLLM) ChatStructured(ctx context.Context, systemPrompt, userMessage string, tool llm.ToolDefinition) (string, string, bool, error) {
	ctx = f.within(ctx)
	c := f.Current()
	payload, modelID, viaTool, usage, err := c.ChatStructured(ctx, systemPrompt, userMessage, tool)
	f.a.recordFacilityCall(wire.TapSource(ctx), c.ModelName(), usage)
	return payload, modelID, viaTool, err
}

func (f facilityLLM) Chat(ctx context.Context, messages []llm.Message, opts llm.ChatOptions) (*llm.Response, error) {
	ctx = f.within(ctx)
	c := f.Current()
	resp, err := c.Chat(ctx, messages, opts)
	f.a.recordFacilityCall(wire.TapSource(wire.WithTapSource(ctx, opts.Source)), c.ModelName(), llm.CallUsage(resp, err))
	return resp, err
}
