package app

import (
	"context"
	"encoding/json"
	"github.com/aiii-dot-id/aii-os/internal/conversation"
	"github.com/aiii-dot-id/aii-os/internal/identity"
	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/prompt"
	"github.com/aiii-dot-id/aii-os/internal/ring"
	"github.com/aiii-dot-id/aii-os/internal/store"
	"github.com/aiii-dot-id/aii-os/internal/tools"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

type pass4Executor struct{}

func (pass4Executor) Execute(_ context.Context, _ llm.ToolCall) conversation.Observation {
	return conversation.Observation{Text: "ok"}
}

type pass4Defs struct{}

func (pass4Defs) ToolDefinitions() []llm.ToolDefinition {
	return []llm.ToolDefinition{{Type: "function", Function: llm.ToolFunction{Name: "read"}}}
}
func TestCacheWakeFailureStillMetersCachedUsage(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests.Add(1) > 1 {
			w.WriteHeader(500)
			w.Write([]byte(`{"error":{"type":"api_error","message":"fixture failure"}}`))
			return
		}
		response := map[string]any{"content": []any{map[string]any{"type": "text", "text": "Checking the timer."}, map[string]any{"type": "tool_use", "id": "c1", "name": "read", "input": map[string]any{}}}, "stop_reason": "tool_use", "usage": map[string]any{"input_tokens": 100, "cache_read_input_tokens": 900, "output_tokens": 10}}
		if err := json.NewEncoder(w).Encode(response); err != nil {
			t.Error(err)
		}
	}))
	t.Cleanup(srv.Close)
	st, err := store.NewMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	rm := ring.NewManager()
	_ = rm.SealSafePosture("test constitution")
	cfg := &Config{Prompt: PromptConfig{MaxTokens: 32000}, Agency: AgencyConfig{MaxToolRounds: 3}}
	a := New(cfg)
	t.Cleanup(a.bgCancel)
	a.store = st
	a.toolReg = tools.NewRegistry(t.TempDir(), nil, tools.Timeouts{})
	a.composer = prompt.New(rm, 32000)
	a.promptGate = prompt.NewGate(appRingSource{rm: rm}, 32000)
	a.engine = identity.NewEngine(st, nil, rm, toolDiscovererAdapter{a.toolReg})
	c := llm.New(&llm.ClientConfig{Endpoint: srv.URL, Provider: "anthropic", Model: "fixture", Retries: -1, TimeoutSeconds: 2})
	a.conv = conversation.New(c, pass4Executor{}, pass4Defs{}, nil, nil, conversation.Config{MaxIterations: 3, ContextBudgetTokens: 32000})
	a.lastTurn = "previous-turn gauge"
	if err := a.acquireTurn(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer a.releaseTurn()
	if _, err := a.wake(t.Context(), "system", "timer fired"); err == nil {
		t.Fatal("fixture expected wake failure")
	}
	if requests.Load() != 2 {
		t.Fatalf("fixture expected two requests; got %d", requests.Load())
	}
	if got := a.lastTurnCost(); !strings.Contains(got, "1010") {
		t.Fatalf("wake traversed compose, cached inference, tool result and failing inference; last-turn gauge remained %q", got)
	}
}
