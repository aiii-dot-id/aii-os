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

// TestHarvestWakeFiresOnDelivery pins CS-1's core acceptance: a
// sub-agent delivery wakes the idle parent into a full cognitive turn
// within seconds, the harvest turn speaks to the operator, and CS-2's
// seen-once marker holds — the outcome is composed once and never
// re-shown by a later compose.
func TestHarvestWakeFiresOnDelivery(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
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
	ex := cognitive.NewExecutor(st)
	ex.SetHolds(a.fg)
	ex.RegisterHandler(&subagentHandler{a: a})
	a.engine = identity.NewEngine(st, nil, rm, toolDiscovererAdapter{reg}, identity.Ports{WorkWake: ex.Wake})
	a.engine.SetAgencyLimits(2, 1, 20, cfg.Agency.SubagentWallSeconds)
	// CS-1's wake runs a resident turn: the conversation loop must be
	// live, wired exactly as buildRuntime wires it.
	a.conv = conversation.New(a.llmSwap, appToolExecutor{a}, appToolDefiner{a},
		appTranscript{st: st}, appEmitter{a: a}, conversation.Config{
			MaxIterations:      cfg.Agency.MaxToolRounds,
			MaxToolResultChars: cfg.Prompt.MaxToolResultChars,
		})

	runCtx, cancel := context.WithCancel(t.Context())
	defer stopHarvestFixture(a, ex, cancel)
	ex.Start(runCtx)

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

	// CS-2 seen-once: the harvest turn's compose marked the outcome.
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

func TestHarvestWakeSurvivesSlowLLM(t *testing.T) {
	content, spoke := slowHarvest(t, 900*time.Millisecond, 5, 10*time.Second)
	if !spoke {
		t.Fatal("slow LLM killed the harvest turn — gate deadline leaked into the turn ctx")
	}
	if !strings.Contains(content, "synthesis complete") {
		t.Fatalf("wake spoke unexpected content: %q", content)
	}
}

func TestAHarvestTurnEndsAtTheOperatorsWall(t *testing.T) {
	if content, spoke := slowHarvest(t, 2500*time.Millisecond, 1, 8*time.Second); spoke {
		t.Fatalf("the harvest's turn ran past a wall of one second and spoke: %q", content)
	}
}

// slowHarvest spawns a sub-agent against a model that answers every request
// after d, with the harvest's wall set to wallSeconds, and reports what the
// harvest turn spoke within wait, or false.
func slowHarvest(t *testing.T, d time.Duration, wallSeconds int, wait time.Duration) (string, bool) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(d)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"r","model":"fake","choices":[{"index":0,"message":{"role":"assistant","content":"synthesis complete: three branches agree"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`)
	}))
	defer srv.Close()

	st, err := store.NewForTest(filepath.Join(t.TempDir(), "aidb-test.db"))
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
		Agency:     AgencyConfig{MaxToolRounds: 2, SubagentWallSeconds: 5, HarvestWallSeconds: wallSeconds},
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
		appTranscript{st: st}, appEmitter{a: a}, conversation.Config{
			MaxIterations:      cfg.Agency.MaxToolRounds,
			MaxToolResultChars: cfg.Prompt.MaxToolResultChars,
		})

	// Shrunk bounds: the LLM delay exceeds the gate wait (200ms) —
	// under the bug, the turn ctx inherited the gate deadline and the
	// call died. The turn's own wall (agency.harvest_wall_seconds)
	// decides whether the synthesis completes.
	prevGate := harvestGateWait
	harvestGateWait = 200 * time.Millisecond
	runCtx, cancel := context.WithCancel(t.Context())
	defer func() {
		// Both the executor and the harvest can read the bound. End and
		// join both lifetimes before restoring it or closing the store.
		stopHarvestFixture(a, ex, cancel)
		harvestGateWait = prevGate
	}()
	ex.Start(runCtx)

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
	call.Function.Arguments = `{"action":"spawn","goal":"verify slow wake"}`
	got := a.executeToolCall(t.Context(), call).Text
	if !strings.Contains(got, "Spawned sub-agent") {
		t.Fatalf("spawn result = %q", got)
	}
	select {
	case content := <-woke:
		return content, true
	case <-time.After(wait):
		return "", false
	}
}

// These fixtures assemble the executor separately from App's runtime.
// Close background admission as Shutdown does, then join both owners
// before a fixture releases its store or changes a shared test bound.
func stopHarvestFixture(a *App, ex *cognitive.Executor, cancel context.CancelFunc) {
	a.bgMu.Lock()
	a.stopping = true
	a.bgCancel()
	a.bgMu.Unlock()
	cancel()
	ex.Stop()
	a.bgWG.Wait()
}

func TestHarvestWakeFailedTurnDoesNotBurnOnce(t *testing.T) {
	var calls int32
	var failFirst atomic.Bool
	failFirst.Store(true)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		if failFirst.Load() {
			// Every call fails until the test says otherwise: the
			// sub-agent's own (its outcome is delivered as FAILED), then
			// the wake turn's — which dies like the live system's
			// deadline kill, after the compose has already rendered the
			// outcome into the prompt.
			http.Error(w, "context deadline exceeded", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"r","model":"fake","choices":[{"index":0,"message":{"role":"assistant","content":"second attempt synthesis"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`)
	}))
	defer srv.Close()

	st, err := store.NewForTest(filepath.Join(t.TempDir(), "aidb-test2.db"))
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
	ex := cognitive.NewExecutor(st)
	ex.SetHolds(a.fg)
	ex.RegisterHandler(&subagentHandler{a: a})
	a.engine = identity.NewEngine(st, nil, rm, toolDiscovererAdapter{reg}, identity.Ports{WorkWake: ex.Wake})
	a.engine.SetAgencyLimits(2, 1, 20, cfg.Agency.SubagentWallSeconds)
	a.conv = conversation.New(a.llmSwap, appToolExecutor{a}, appToolDefiner{a},
		appTranscript{st: st}, appEmitter{a: a}, conversation.Config{
			MaxIterations:      cfg.Agency.MaxToolRounds,
			MaxToolResultChars: cfg.Prompt.MaxToolResultChars,
		})

	runCtx, cancel := context.WithCancel(t.Context())
	defer stopHarvestFixture(a, ex, cancel)
	ex.Start(runCtx)

	var call llm.ToolCall
	call.Type = "function"
	call.Function.Name = "work"
	call.Function.Arguments = `{"action":"spawn","goal":"once-ness probe"}`
	got := a.executeToolCall(t.Context(), call).Text
	if !strings.Contains(got, "Spawned sub-agent") {
		t.Fatalf("spawn result = %q", got)
	}

	waitUntil(t, func() bool { return atomic.LoadInt32(&calls) >= 2 }, "the wake turn's call")
	if err := a.acquireTurn(t.Context()); err != nil {
		t.Fatal(err)
	}

	// THE ASSERTION: the outcome must still be unharvested — the
	// failed turn did not earn the once-ness marker.
	unh, err := st.UnharvestedDeliveries(8)
	if err != nil {
		t.Fatal(err)
	}
	if len(unh) == 0 {
		t.Fatal("failed wake turn burned the once-ness marker — the outcome is lost to synthesis forever (live 13:28:35 defect)")
	}

	// And it resurfaces: the next turn composes it again, and THAT
	// successful turn marks it.
	failFirst.Store(false)
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
}
