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

	"github.com/aiii-dot-id/aii-os/internal/conversation"
	"github.com/aiii-dot-id/aii-os/internal/identity"
	"github.com/aiii-dot-id/aii-os/internal/limits"
	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/prompt"
	"github.com/aiii-dot-id/aii-os/internal/ring"
	"github.com/aiii-dot-id/aii-os/internal/store"
	"github.com/aiii-dot-id/aii-os/internal/tools"
)

func TestTimerWakeTurnHasBudget(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		// Slower than the shrunk budget; a caller that gives up ends the wait.
		select {
		case <-r.Context().Done():
			return
		case <-time.After(3500 * time.Millisecond):
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"r","model":"fake","choices":[{"index":0,"message":{"role":"assistant","content":"an alarm answer that took too long"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`)
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
		// The turn's wall, agency.alarm_wall_seconds: 2s, under the LLM's
		// 3.5s latency.
		Agency: AgencyConfig{MaxToolRounds: limits.Of(2), SubagentWallSeconds: 5, AlarmWallSeconds: 2},
	}
	a := New(cfg)
	a.store = st
	a.toolReg = reg
	a.composer = prompt.New(rm, cfg.Prompt.MaxTokens)
	a.promptGate = prompt.NewGate(appRingSource{rm: rm}, cfg.Prompt.MaxTokens)
	a.llmSwap = newSwappableLLM(llm.New(&llm.ClientConfig{
		Endpoint: srv.URL, Model: "fake", MaxOutputTokens: 64, Retries: -1,
	}))
	a.engine = identity.NewEngine(st, nil, rm, toolDiscovererAdapter{reg}, identity.Ports{})
	a.conv = conversation.New(a.llmSwap, appToolExecutor{a}, appToolDefiner{a},
		appTranscript{st: st}, appEmitter{a: a}, testLoop(conversation.Config{
			MaxIterations:      cfg.Agency.MaxToolRounds,
			MaxToolResultChars: cfg.Prompt.MaxToolResultChars,
		}))

	done := make(chan struct{})
	go func() {
		defer close(done)
		a.wakeTimerAlarm(context.Background(), "budgetpin", timerNotice("budgetpin", "ops", "check the log rotation", time.Now()))
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("wakeTimerAlarm did not return — the turn is unbounded")
	}

	msgs, err := st.UndeliveredMessages()
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range msgs {
		if strings.HasPrefix(m.ID, "wake_budgetpin_") {
			t.Fatalf("slow wake completed under a %s wall — the turn ctx is unbounded (spoke: %q)", a.turnWall(alarmWallSetting), m.Content)
		}
	}
	if atomic.LoadInt32(&calls) == 0 {
		t.Fatal("the LLM was never called — the test did not exercise the turn")
	}
}
