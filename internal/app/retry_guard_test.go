package app

import (
	"context"
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

// .
// .
// .
// .
// .
// .
func TestFailedSubagentRunIsDeliveredOnceAndNeverRetried(t *testing.T) {
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer fake.Close()

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

	var call llm.ToolCall
	call.Type = "function"
	call.Function.Name = "work"
	call.Function.Arguments = `{"action":"spawn","goal":"fail after starting"}`
	if got := a.executeToolCall(t.Context(), call).Text; !strings.Contains(got, "Spawned sub-agent") {
		t.Fatalf("spawn result = %q", got)
	}

	// .
	// .
	deadline := time.Now().Add(5 * time.Second)
	var state string
	var retries int
	for time.Now().Before(deadline) {
		row := st.DB().QueryRow(`SELECT state, retry_count FROM work_queue ORDER BY created_ms DESC LIMIT 1`)
		if err := row.Scan(&state, &retries); err == nil && state != "PENDING" && state != "CLAIMED" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if state != "DONE" || retries != 0 {
		t.Fatalf("queue item ended (state=%q, retries=%d), want (DONE, 0) — a begun trajectory must complete, not retry", state, retries)
	}
	sessions, err := st.RecentDeliveredSubagents(1)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || !strings.Contains(sessions[0].Result, "FAILED:") {
		t.Fatalf("delivered sessions = %+v — the failure must be delivered as the outcome exactly once", sessions)
	}

	// .
	// .
	// .
	runs, _, _, failedRuns, err := st.SubagentStats(time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if runs != 1 || failedRuns != 1 {
		t.Fatalf("subagent_metrics holds (runs=%d, failed=%d), want (1, 1) — the child run left no cost trace", runs, failedRuns)
	}
}

// .
// .
// .
func TestDeliveryRefusesToOverwriteARealResult(t *testing.T) {
	st, err := store.New(filepath.Join(t.TempDir(), "aii.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	if err := st.StartWorkSession("ws_law", "the delivery law"); err != nil {
		t.Fatal(err)
	}
	if err := st.DeliverWorkSession("ws_law", "the real outcome", "", ""); err != nil {
		t.Fatalf("first delivery must land: %v", err)
	}
	if err := st.DeliverWorkSession("ws_law", "a replayed outcome", "", ""); err == nil {
		t.Fatal("a second delivery over a real result landed — trajectory replay reached the record")
	}
	var got string
	if err := st.DB().QueryRow(`SELECT result FROM work_sessions WHERE id='ws_law'`).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != "the real outcome" {
		t.Fatalf("result = %q, the refusal must leave the first outcome untouched", got)
	}

	if err := st.StartWorkSession("ws_early", "pre-exec retry path"); err != nil {
		t.Fatal(err)
	}
	if err := st.DeliverWorkSession("ws_early", "unserved: failed before start: compose blew up", "", ""); err != nil {
		t.Fatal(err)
	}
	if err := st.DeliverWorkSession("ws_early", "the retry's real outcome", "", ""); err != nil {
		t.Fatalf("replacing the pre-execution marker is the lawful path and must stay open: %v", err)
	}
}

// .
// .
// .
func TestWorkStatusNamesDeliveredUnharvestedWork(t *testing.T) {
	st, err := store.New(filepath.Join(t.TempDir(), "aii.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	rm := ring.NewManager()
	_ = rm.SealSafePosture("test constitution")
	rm.Set(ring.Ring5, &ring.RingContent{Level: ring.Ring5, Content: "test floor"})
	reg := tools.NewRegistry(t.TempDir(), nil, tools.Timeouts{})
	a := New(&Config{
		SourcePath: filepath.Join(t.TempDir(), "config.json"),
		Prompt:     PromptConfig{MaxTokens: 32000, MaxToolResultChars: 32000},
		Agency:     AgencyConfig{MaxToolRounds: 2},
	})
	a.store = st
	a.toolReg = reg
	a.engine = identity.NewEngine(st, nil, rm, toolDiscovererAdapter{reg})

	if _, enq, err := st.EnqueueWorkWithSessionBelowLimit(&store.WorkItem{Kind: identity.SubagentWorkKind, Payload: "{}", DedupKey: "ws_stat", Source: "identity", LeaseMs: 1000},
		4, "ws_stat", store.SubagentDescription("survey the logs")); err != nil || !enq {
		t.Fatalf("enqueue: enq=%v err=%v", enq, err)
	}
	if err := st.DeliverWorkSession("ws_stat", "found three things", "", ""); err != nil {
		t.Fatal(err)
	}

	var call llm.ToolCall
	call.Type = "function"
	call.Function.Name = "work"
	call.Function.Arguments = `{"action":"status"}`
	got := a.executeToolCall(t.Context(), call)
	if got.Failed {
		t.Fatalf("work status failed: %s", got.Text)
	}
	if !strings.Contains(got.Text, "delivered ws_stat") || !strings.Contains(got.Text, "survey the logs") {
		t.Fatalf("status omits the delivered worker: %q", got.Text)
	}
}

// .
// .
// .
func TestSubagentEventsCarryTheActorLabel(t *testing.T) {
	a := New(&Config{SourcePath: filepath.Join(t.TempDir(), "config.json")})
	var gotArgs []string
	a.toolEmit = func(kind, name, args string) { gotArgs = append(gotArgs, args) }

	appEmitter{a: a, actor: "ws_label"}.EmitToolEvent("call", "read", `{"f":1}`)
	appEmitter{a: a}.EmitToolEvent("call", "read", `{"f":2}`)

	if len(gotArgs) != 2 || !strings.HasPrefix(gotArgs[0], "[ws_label] ") {
		t.Fatalf("sub-agent event not labeled: %v", gotArgs)
	}
	if strings.HasPrefix(gotArgs[1], "[") {
		t.Fatalf("the resident's own event must stay unlabeled: %v", gotArgs)
	}
}

// .
// .
// .
func TestWorkYieldSetsEndTurnAtTheExecutorSeam(t *testing.T) {
	st, err := store.New(filepath.Join(t.TempDir(), "aii.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	rm := ring.NewManager()
	_ = rm.SealSafePosture("test constitution")
	rm.Set(ring.Ring5, &ring.RingContent{Level: ring.Ring5, Content: "test floor"})
	reg := tools.NewRegistry(t.TempDir(), nil, tools.Timeouts{})
	a := New(&Config{
		SourcePath: filepath.Join(t.TempDir(), "config.json"),
		Prompt:     PromptConfig{MaxTokens: 32000, MaxToolResultChars: 32000},
		Agency:     AgencyConfig{MaxToolRounds: 2},
	})
	a.store = st
	a.toolReg = reg
	a.engine = identity.NewEngine(st, nil, rm, toolDiscovererAdapter{reg})

	var call llm.ToolCall
	call.Type = "function"
	call.Function.Name = "work"
	call.Function.Arguments = `{"action":"yield"}`
	obs := a.executeToolCall(t.Context(), call)
	if obs.Failed || !obs.EndTurn {
		t.Fatalf("work yield → Observation{Failed:%v, EndTurn:%v}, want clean EndTurn; text: %s", obs.Failed, obs.EndTurn, obs.Text)
	}

	call.Function.Arguments = `{"action":"status"}`
	if obs := a.executeToolCall(t.Context(), call); obs.EndTurn {
		t.Fatal("work status must not end the turn")
	}
}

// .
// .
func TestComposeRendersRunningWorkerTruth(t *testing.T) {
	st, err := store.New(filepath.Join(t.TempDir(), "aii.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	a := New(&Config{SourcePath: filepath.Join(t.TempDir(), "config.json")})
	a.store = st

	if _, enq, err := st.EnqueueWorkWithSessionBelowLimit(&store.WorkItem{Kind: identity.SubagentWorkKind, Payload: "{}", DedupKey: "ws_run", Source: "identity", LeaseMs: 60000},
		4, "ws_run", store.SubagentDescription("count the beans")); err != nil || !enq {
		t.Fatalf("enqueue: %v %v", enq, err)
	}

	state, err := a.buildWorkState()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(state, "Sub-agents running now") || !strings.Contains(state, "count the beans") {
		t.Fatalf("compose omits the running worker: %q", state)
	}
	if !strings.Contains(state, "work yield") {
		t.Error("the running-worker block does not teach the yield")
	}
}

// .
// .
// .
// .
// .
// .
// .
func TestSubagentCannotYieldTheTurn(t *testing.T) {
	st, err := store.New(filepath.Join(t.TempDir(), "aii.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	rm := ring.NewManager()
	_ = rm.SealSafePosture("test constitution")
	rm.Set(ring.Ring5, &ring.RingContent{Level: ring.Ring5, Content: "test floor"})
	reg := tools.NewRegistry(t.TempDir(), nil, tools.Timeouts{})
	a := New(&Config{
		SourcePath: filepath.Join(t.TempDir(), "config.json"),
		Prompt:     PromptConfig{MaxTokens: 32000, MaxToolResultChars: 32000},
		Agency:     AgencyConfig{MaxToolRounds: 2},
	})
	a.store = st
	a.toolReg = reg
	a.engine = identity.NewEngine(st, nil, rm, toolDiscovererAdapter{reg})

	var call llm.ToolCall
	call.Type = "function"
	call.Function.Name = "work"
	call.Function.Arguments = `{"action":"yield"}`

	// .
	if obs := a.executeToolCall(t.Context(), call); !obs.EndTurn || obs.Failed {
		t.Fatalf("the resident lost its yield: Failed=%v EndTurn=%v", obs.Failed, obs.EndTurn)
	}

	// .
	// .
	subCtx := context.WithValue(t.Context(), identity.SubagentDepth{}, 1)
	obs := a.executeToolCall(subCtx, call)
	if obs.EndTurn {
		t.Fatal("a sub-agent yielded the turn — its run would end early and deliver a fragment")
	}
	if !obs.Failed || !strings.Contains(obs.Text, "deliverable") {
		t.Fatalf("the refusal does not name the right move: Failed=%v text=%q", obs.Failed, obs.Text)
	}
}
