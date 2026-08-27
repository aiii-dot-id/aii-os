package app

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/cognitive"
	"github.com/aiii-dot-id/aii-os/internal/identity"
	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/prompt"
	"github.com/aiii-dot-id/aii-os/internal/ring"
	"github.com/aiii-dot-id/aii-os/internal/store"
	"github.com/aiii-dot-id/aii-os/internal/tools"
)

func TestWorkSpawnRunsAndDelivers(t *testing.T) {
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{
			"id":"subagent-test","model":"fake-subagent",
			"choices":[{"index":0,"message":{"role":"assistant","content":"independent review complete"},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":10,"completion_tokens":3,"total_tokens":13}
		}`)
	}))
	defer fake.Close()

	st, err := store.New(filepath.Join(t.TempDir(), "aii.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	rm := ring.NewManager()
	rm.Set(ring.Ring0, &ring.RingContent{Level: ring.Ring0, Content: "test constitution"})
	rm.Set(ring.Ring5, &ring.RingContent{Level: ring.Ring5, Content: "test floor"})
	reg := tools.NewRegistry(t.TempDir(), nil, tools.Timeouts{})
	cfg := &Config{
		SourcePath: filepath.Join(t.TempDir(), "config.json"),
		Prompt:     PromptConfig{MaxTokens: 32000, MaxToolResultChars: 32000},
		Agency:     AgencyConfig{MaxToolRounds: 2, SubagentWallSeconds: 5},
	}
	a := New(cfg)
	a.store = st
	a.toolReg = reg
	a.composer = prompt.New(rm, cfg.Prompt.MaxTokens)
	a.promptGate = prompt.NewGate(appRingSource{rm}, cfg.Prompt.MaxTokens)
	a.llmSwap = newSwappableLLM(llm.New(&llm.ClientConfig{
		Endpoint: fake.URL, Model: "fake-subagent", MaxOutputTokens: 64, Retries: -1,
	}))
	a.engine = identity.NewEngine(st, nil, rm, toolDiscovererAdapter{reg})
	a.engine.SetAgencyLimits(2, 1, 20, cfg.Agency.SubagentWallSeconds)

	ex := cognitive.NewExecutor(st)
	ex.SetHolds(a.fg)
	ex.RegisterHandler(&subagentHandler{a: a})
	a.engine.SetWorkWake(ex.Wake)
	runCtx, cancel := context.WithCancel(t.Context())
	defer cancel()
	ex.Start(runCtx)
	defer ex.Stop()

	delivered := make(chan struct{}, 1)
	st.OnOutboxWrite(func() {
		select {
		case delivered <- struct{}{}:
		default:
		}
	})
	var call llm.ToolCall
	call.Type = "function"
	call.Function.Name = "work"
	call.Function.Arguments = `{"action":"spawn","goal":"review the boundary"}`
	got := a.executeToolCall(t.Context(), call)
	if !strings.Contains(got, "Spawned sub-agent") {
		t.Fatalf("spawn result = %q", got)
	}
	select {
	case <-delivered:
	case <-time.After(2 * time.Second):
		t.Fatal("spawned work was not woken and delivered")
	}
	sessions, err := st.RecentDeliveredSubagents(1)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || !strings.Contains(sessions[0].Result, "independent review complete") {
		t.Fatalf("delivered sessions = %+v", sessions)
	}
}
