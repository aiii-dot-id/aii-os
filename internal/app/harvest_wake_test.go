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
	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/prompt"
	"github.com/aiii-dot-id/aii-os/internal/ring"
	"github.com/aiii-dot-id/aii-os/internal/store"
	"github.com/aiii-dot-id/aii-os/internal/tools"
)

// .
// .
// .
// .
// .
func TestHarvestWakeFiresOnDelivery(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"r","model":"fake","choices":[{"index":0,"message":{"role":"assistant","content":"harvested: the review is complete and filed"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`)
	}))
	defer srv.Close()

	st, err := store.New(filepath.Join(t.TempDir(), "aii.db"))
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
		Agency:     AgencyConfig{MaxToolRounds: 2, SubagentWallSeconds: 5},
	}
	a := New(cfg)
	a.store = st
	a.toolReg = reg
	a.composer = prompt.New(rm, cfg.Prompt.MaxTokens)
	a.promptGate = prompt.NewGate(appRingSource{rm: rm}, cfg.Prompt.MaxTokens)
	a.llmSwap = newSwappableLLM(llm.New(&llm.ClientConfig{
		Endpoint: srv.URL, Model: "fake", MaxOutputTokens: 64, Retries: -1,
	}))
	a.engine = identity.NewEngine(st, nil, rm, toolDiscovererAdapter{reg})
	a.engine.SetAgencyLimits(2, 1, 20, cfg.Agency.SubagentWallSeconds)
	// .
	// .
	a.conv = conversation.New(a.llmSwap, appToolExecutor{a}, appToolDefiner{a},
		appTranscript{st: st}, appEmitter{a: a}, conversation.Config{
			MaxIterations:      cfg.Agency.MaxToolRounds,
			MaxToolResultChars: cfg.Prompt.MaxToolResultChars,
		})

	ex := cognitive.NewExecutor(st)
	ex.SetHolds(a.fg)
	ex.RegisterHandler(&subagentHandler{a: a})
	a.engine.SetWorkWake(ex.Wake)
	runCtx, cancel := context.WithCancel(t.Context())
	defer cancel()
	ex.Start(runCtx)
	defer ex.Stop()

	woke := make(chan string, 4)
	st.OnOutboxWrite(func() {
		msgs, _ := st.UndeliveredMessages()
		for _, m := range msgs {
			if strings.HasPrefix(m.ID, "wake_subagent_") && !strings.Contains(m.ID, "_safe") {
				select {
				case woke <- m.Content:
				default:
				}
			}
		}
	})

	var call llm.ToolCall
	call.Type = "function"
	call.Function.Name = "work"
	call.Function.Arguments = `{"action":"spawn","goal":"review the boundary"}`
	got := a.executeToolCall(t.Context(), call).Text
	if !strings.Contains(got, "Spawned sub-agent") {
		t.Fatalf("spawn result = %q", got)
	}
	select {
	case content := <-woke:
		if !strings.Contains(content, "harvested") {
			t.Fatalf("wake spoke unexpected content: %q", content)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("delivery did not wake the parent within 10s")
	}

	// .
	unh, err := st.UnharvestedDeliveries(8)
	if err != nil {
		t.Fatal(err)
	}
	if len(unh) != 0 {
		t.Fatalf("post-wake unharvested = %d (want 0 — compose-time marking)", len(unh))
	}
	ws, err := a.buildWorkState()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(ws, "review the boundary") {
		t.Fatal("a later compose re-showed the harvested outcome")
	}
}

// .
// .
// .
// .
// .
// .
// .
// .
// .
func TestHarvestWakeSurvivesSlowLLM(t *testing.T) {
	d := 900 * time.Millisecond
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(d)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"r","model":"fake","choices":[{"index":0,"message":{"role":"assistant","content":"synthesis complete: three branches agree"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`)
	}))
	defer srv.Close()

	st, err := store.New(filepath.Join(t.TempDir(), "aidb-test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	rm := ring.NewManager()
	_ = rm.SealSafePosture("test constitution")
	rm.Set(ring.Ring5, &ring.RingContent{Level: ring.Ring5, Content: "test floor"})
	reg := tools.NewRegistry(t.TempDir(), nil, tools.Timeouts{})
	cfg := &Config{
		SourcePath: filepath.Join(t.TempDir(), "cfg-test.json"),
		Prompt:     PromptConfig{MaxTokens: 32000, MaxToolResultChars: 32000},
		Agency:     AgencyConfig{MaxToolRounds: 2, SubagentWallSeconds: 5},
	}
	a := New(cfg)
	a.store = st
	a.toolReg = reg
	a.composer = prompt.New(rm, cfg.Prompt.MaxTokens)
	a.promptGate = prompt.NewGate(appRingSource{rm: rm}, cfg.Prompt.MaxTokens)
	a.llmSwap = newSwappableLLM(llm.New(&llm.ClientConfig{
		Endpoint: srv.URL, Model: "fake", MaxOutputTokens: 64, Retries: -1,
	}))
	a.engine = identity.NewEngine(st, nil, rm, toolDiscovererAdapter{reg})
	a.engine.SetAgencyLimits(2, 1, 20, cfg.Agency.SubagentWallSeconds)
	a.conv = conversation.New(a.llmSwap, appToolExecutor{a}, appToolDefiner{a},
		appTranscript{st: st}, appEmitter{a: a}, conversation.Config{
			MaxIterations:      cfg.Agency.MaxToolRounds,
			MaxToolResultChars: cfg.Prompt.MaxToolResultChars,
		})

	ex := cognitive.NewExecutor(st)
	ex.SetHolds(a.fg)
	ex.RegisterHandler(&subagentHandler{a: a})
	a.engine.SetWorkWake(ex.Wake)
	runCtx, cancel := context.WithCancel(t.Context())
	defer cancel()
	ex.Start(runCtx)
	defer ex.Stop()

	woke := make(chan string, 4)
	st.OnOutboxWrite(func() {
		msgs, _ := st.UndeliveredMessages()
		for _, m := range msgs {
			if strings.HasPrefix(m.ID, "wake_subagent_") && !strings.Contains(m.ID, "_safe") {
				select {
				case woke <- m.Content:
				default:
				}
			}
		}
	})

	// .
	// .
	// .
	// .
	prevGate, prevBudget := harvestGateWait, harvestTurnBudget
	harvestGateWait, harvestTurnBudget = 200*time.Millisecond, 5*time.Second
	defer func() { harvestGateWait, harvestTurnBudget = prevGate, prevBudget }()

	var call llm.ToolCall
	call.Type = "function"
	call.Function.Name = "work"
	call.Function.Arguments = `{"action":"spawn","goal":"verify slow wake"}`
	got := a.executeToolCall(t.Context(), call).Text
	if !strings.Contains(got, "Spawned sub-agent") {
		t.Fatalf("spawn result = %q", got)
	}
	select {
	case content := <-woke:
		if !strings.Contains(content, "synthesis complete") {
			t.Fatalf("wake spoke unexpected content: %q", content)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("slow LLM killed the harvest turn — gate deadline leaked into the turn ctx")
	}
}

// .
// .
// .
// .
// .
// .
// .
// .
func TestHarvestWakeFailedTurnDoesNotBurnOnce(t *testing.T) {
	var calls int32
	var failFirst atomic.Bool
	failFirst.Store(true)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if failFirst.Load() {
			// .
			// .
			// .
			http.Error(w, "context deadline exceeded", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"r","model":"fake","choices":[{"index":0,"message":{"role":"assistant","content":"second attempt synthesis"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`)
	}))
	defer srv.Close()

	st, err := store.New(filepath.Join(t.TempDir(), "aidb-test2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	rm := ring.NewManager()
	_ = rm.SealSafePosture("test constitution")
	rm.Set(ring.Ring5, &ring.RingContent{Level: ring.Ring5, Content: "test floor"})
	reg := tools.NewRegistry(t.TempDir(), nil, tools.Timeouts{})
	cfg := &Config{
		SourcePath: filepath.Join(t.TempDir(), "cfg-test2.json"),
		Prompt:     PromptConfig{MaxTokens: 32000, MaxToolResultChars: 32000},
		Agency:     AgencyConfig{MaxToolRounds: 2, SubagentWallSeconds: 5},
	}
	a := New(cfg)
	a.store = st
	a.toolReg = reg
	a.composer = prompt.New(rm, cfg.Prompt.MaxTokens)
	a.promptGate = prompt.NewGate(appRingSource{rm: rm}, cfg.Prompt.MaxTokens)
	a.llmSwap = newSwappableLLM(llm.New(&llm.ClientConfig{
		Endpoint: srv.URL, Model: "fake", MaxOutputTokens: 64, Retries: -1,
	}))
	a.engine = identity.NewEngine(st, nil, rm, toolDiscovererAdapter{reg})
	a.engine.SetAgencyLimits(2, 1, 20, cfg.Agency.SubagentWallSeconds)
	a.conv = conversation.New(a.llmSwap, appToolExecutor{a}, appToolDefiner{a},
		appTranscript{st: st}, appEmitter{a: a}, conversation.Config{
			MaxIterations:      cfg.Agency.MaxToolRounds,
			MaxToolResultChars: cfg.Prompt.MaxToolResultChars,
		})

	ex := cognitive.NewExecutor(st)
	ex.SetHolds(a.fg)
	ex.RegisterHandler(&subagentHandler{a: a})
	a.engine.SetWorkWake(ex.Wake)
	runCtx, cancel := context.WithCancel(t.Context())
	defer cancel()
	ex.Start(runCtx)
	defer ex.Stop()

	var call llm.ToolCall
	call.Type = "function"
	call.Function.Name = "work"
	call.Function.Arguments = `{"action":"spawn","goal":"once-ness probe"}`
	got := a.executeToolCall(t.Context(), call).Text
	if !strings.Contains(got, "Spawned sub-agent") {
		t.Fatalf("spawn result = %q", got)
	}

	// .
	// .
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if atomic.LoadInt32(&calls) >= 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	time.Sleep(500 * time.Millisecond)

	// .
	// .
	unh, err := st.UnharvestedDeliveries(8)
	if err != nil {
		t.Fatal(err)
	}
	if len(unh) == 0 {
		t.Fatal("failed wake turn burned the once-ness marker — the outcome is lost to synthesis forever (live 13:28:35 defect)")
	}

	// .
	// .
	failFirst.Store(false)
	if err := a.acquireTurn(t.Context()); err != nil {
		t.Fatal(err)
	}
	spoken, err := a.wake(t.Context(), "system", "second attempt: harvest now")
	a.releaseTurn()
	if err != nil {
		t.Fatalf("second turn failed: %v", err)
	}
	_ = spoken
	unh2, err := st.UnharvestedDeliveries(8)
	if err != nil {
		t.Fatal(err)
	}
	if len(unh2) != 0 {
		t.Fatalf("successful turn did not mark the outcome it composed: %d left", len(unh2))
	}
	_ = calls
}
