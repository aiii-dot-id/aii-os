package app

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/cognitive"
	"github.com/aiii-dot-id/aii-os/internal/conversation"
	"github.com/aiii-dot-id/aii-os/internal/identity"
	"github.com/aiii-dot-id/aii-os/internal/limits"
	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/prompt"
	"github.com/aiii-dot-id/aii-os/internal/ring"
	"github.com/aiii-dot-id/aii-os/internal/store"
	"github.com/aiii-dot-id/aii-os/internal/tools"
)

func TestHarvestWakeCoalescesOnSweptSet(t *testing.T) {
	var llmCalls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		llmCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"r","model":"fake","choices":[{"index":0,"message":{"role":"assistant","content":"harvested: the review is complete and filed"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`)
	}))
	defer srv.Close()

	st, err := store.NewForTest(filepath.Join(t.TempDir(), "aii.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	rm := ring.NewManager()
	_ = rm.SealSafePosture("test constitution")
	rm.Set(ring.Ring5, &ring.RingContent{Level: ring.Ring5, Content: "test floor"})
	reg := tools.NewRegistry(t.TempDir(), nil, tools.Timeouts{})
	cfg := &Config{
		SourcePath: filepath.Join(t.TempDir(), "config.json"),
		Prompt:     PromptConfig{MaxTokens: 32000, MaxToolResultChars: 32000},
		Agency:     AgencyConfig{MaxToolRounds: limits.Of(2), SubagentWallSeconds: 5},
	}
	a := New(cfg)
	a.store = st
	a.toolReg = reg
	a.composer = prompt.New(rm, cfg.Prompt.MaxTokens)
	a.promptGate = prompt.NewGate(appRingSource{rm: rm}, cfg.Prompt.MaxTokens)
	a.llmSwap = newSwappableLLM(llm.New(&llm.ClientConfig{
		Endpoint: srv.URL, Model: "fake", MaxOutputTokens: 64, Retries: -1,
	}))
	ex := cognitive.NewExecutor(st)
	ex.SetHolds(a.fg)
	ex.RegisterHandler(&subagentHandler{a: a})
	a.engine = identity.NewEngine(st, nil, rm, toolDiscovererAdapter{reg}, identity.Ports{WorkWake: ex.Wake})
	a.engine.SetAgencyLimits(2, 1, 20, cfg.Agency.SubagentWallSeconds)
	a.conv = conversation.New(a.llmSwap, appToolExecutor{a}, appToolDefiner{a},
		appTranscript{st: st}, appEmitter{a: a}, testLoop(conversation.Config{
			MaxIterations:      cfg.Agency.MaxToolRounds,
			MaxToolResultChars: cfg.Prompt.MaxToolResultChars,
		}))

	runCtx, cancel := context.WithCancel(t.Context())
	defer cancel()
	ex.Start(runCtx)
	defer ex.Stop()

	// Phase 1: spawn → delivery → wake → harvest sweeps the set.
	var call llm.ToolCall
	call.Type = "function"
	call.Function.Name = "work"
	call.Function.Arguments = `{"action":"spawn","goal":"review the boundary"}`
	got := a.executeToolCall(t.Context(), call).Text
	if !strings.Contains(got, "Spawned sub-agent") {
		t.Fatalf("spawn result = %q", got)
	}
	swept := false
	for range 150 {
		unh, err := st.UnharvestedDeliveries(8)
		if err != nil {
			t.Fatal(err)
		}
		wakeSpoken := false
		msgs, _ := st.UndeliveredMessages()
		for _, m := range msgs {
			if strings.HasPrefix(m.ID, "wake_subagent_ws_") && !strings.Contains(m.ID, "_safe") && m.Content != "" {
				wakeSpoken = true
			}
		}
		// The harvest turn is COMPLETE when the set is swept AND its
		// spoken synthesis has landed — "complete" is not observable
		// mid-flight from marking alone.
		if len(unh) == 0 && wakeSpoken {
			swept = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !swept {
		t.Fatal("first delivery never woke/harvested within 15s")
	}
	// Drain residual in-flight motion: the LLM must be quiet.
	time.Sleep(300 * time.Millisecond)

	// Phase 2: the set is swept. A wake that takes the gate NOW must
	// stand down — the observable is the LLM call count: it must not
	// move, and no outbox message may carry this wake's session ID.
	before := llmCalls.Load()
	a.wakeSubagentDelivery(t.Context(), "ws_swept_set", "already harvested goal")
	time.Sleep(500 * time.Millisecond)
	if after := llmCalls.Load(); after != before {
		t.Fatalf("wake on swept set ran an LLM turn: %d calls before, %d after (want equal)", before, after)
	}
	msgs, err := st.UndeliveredMessages()
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range msgs {
		if strings.HasPrefix(m.ID, "wake_subagent_ws_swept_set") {
			t.Fatalf("wake on swept set wrote to the outbox: %q", m.ID)
		}
	}
}
