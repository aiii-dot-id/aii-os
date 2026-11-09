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
	"github.com/aiii-dot-id/aii-os/internal/conversation"
	"github.com/aiii-dot-id/aii-os/internal/identity"
	"github.com/aiii-dot-id/aii-os/internal/interaction"
	"github.com/aiii-dot-id/aii-os/internal/limits"
	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/prompt"
	"github.com/aiii-dot-id/aii-os/internal/ring"
	"github.com/aiii-dot-id/aii-os/internal/store"
	"github.com/aiii-dot-id/aii-os/internal/tools"
)

// A DELIVERED SESSION'S REPORT FROM A HARVEST WAKE REACHES THE OUTBOX:
// the parent waits on its child's delivery, the harvest wakes it, it delivers its own
// work, chooses to speak, and reports. The report is sent, not held.
func TestADeliveredSessionsReportFromAHarvestWakeReachesTheOutbox(t *testing.T) {
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n++
		w.Header().Set("Content-Type", "application/json")
		switch n {
		case 1: // the child's one turn
			_, _ = io.WriteString(w, textReply("the review is complete and filed"))
		case 2: // the parent, woken: delivers, chooses to speak
			_, _ = io.WriteString(w, callReply("",
				toolCall("p1", "work", `{"action":"deliver","result":"served: all three done in sequence"}`),
				toolCall("p2", "work", `{"action":"say","choice":"speak"}`)))
		default:
			_, _ = io.WriteString(w, textReply("report: all three done in sequence"))
		}
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
		Agency:     AgencyConfig{MaxToolRounds: limits.Of(4), SubagentWallSeconds: 5},
	}
	a := New(cfg)
	a.store = st
	a.toolReg = reg
	a.composer = prompt.New(rm, cfg.Prompt.MaxTokens)
	a.promptGate = prompt.NewGate(appRingSource{rm: rm}, cfg.Prompt.MaxTokens)
	a.llmSwap = newSwappableLLM(llm.New(&llm.ClientConfig{Endpoint: srv.URL, Model: "fake", MaxOutputTokens: 256, Retries: -1}))
	ex := cognitive.NewExecutor(st)
	ex.SetHolds(a.fg)
	ex.RegisterHandler(&subagentHandler{a: a})
	a.engine = identity.NewEngine(st, nil, rm, toolDiscovererAdapter{reg}, identity.Ports{WorkWake: ex.Wake})
	a.engine.SetAgencyLimits(2, 1, 20, cfg.Agency.SubagentWallSeconds)
	a.conv = conversation.New(a.llmSwap, appToolExecutor{a}, appToolDefiner{a}, appTranscript{st: st}, appEmitter{a: a},
		testLoop(conversation.Config{MaxIterations: cfg.Agency.MaxToolRounds, MaxToolResultChars: cfg.Prompt.MaxToolResultChars}))
	runCtx, cancel := context.WithCancel(t.Context())
	defer stopHarvestFixture(a, ex, cancel)

	// The parent's own session, waiting on the child it spawns.
	ctx := interaction.WithTurnSource(interaction.WithTurn(t.Context(), "turn_op"), turnSourceOperator)
	if err := st.StartWorkSession(ctx, "ws_parent", "do all three, then report"); err != nil {
		t.Fatal(err)
	}
	var call llm.ToolCall
	call.Type = "function"
	call.Function.Name = "work"
	call.Function.Arguments = `{"action":"spawn","goal":"review the boundary"}`
	got := a.executeToolCall(ctx, call).Text
	if !strings.Contains(got, "Spawned sub-agent") {
		t.Fatalf("spawn result = %q", got)
	}
	child := ""
	for _, w := range strings.Fields(got) {
		if strings.HasPrefix(w, "ws_") {
			child = strings.TrimRight(w, ".,;:")
			break
		}
	}
	if child == "" {
		t.Fatalf("no child id in %q", got)
	}
	on := "delivery:" + child
	if err := st.SetWorkPromise(ctx, "ws_parent", nil, &on, "the parent waits on the child"); err != nil {
		t.Fatal(err)
	}
	ex.Start(runCtx)

	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		msgs, _ := st.UndeliveredFor("operator")
		for _, m := range msgs {
			if strings.HasPrefix(m.ID, "wake_subagent_") && strings.Contains(m.Content, "report: all three done in sequence") {
				if r := rowBeside(t, a); r != "" {
					t.Fatalf("a row beside the sent report: %q", r)
				}
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("the delivered session's report did not reach the outbox (row beside: %q)", rowBeside(t, a))
}
