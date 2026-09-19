package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
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
func scriptedChild(t *testing.T, toolRounds int) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		k := n.Add(1)
		w.Header().Set("Content-Type", "application/json")
		if int(k) <= toolRounds {
			_, _ = io.WriteString(w, `{"id":"c","model":"fake-child","choices":[{"index":0,"message":{"role":"assistant","content":"","tool_calls":[{"id":"call_`+fmt.Sprint(k)+`","type":"function","function":{"name":"ls","arguments":"{\"path\":\".\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":100,"completion_tokens":10,"total_tokens":110}}`)
			return
		}
		_, _ = io.WriteString(w, `{"id":"c","model":"fake-child","choices":[{"index":0,"message":{"role":"assistant","content":"VERDICT: done in leg two"},"finish_reason":"stop"}],"usage":{"prompt_tokens":100,"completion_tokens":5,"total_tokens":105}}`)
	}))
	t.Cleanup(srv.Close)
	return srv, &n
}

func legFixture(t *testing.T, srvURL string, agency AgencyConfig) (*App, *store.Store, *cognitive.Executor) {
	t.Helper()
	return legFixtureBudget(t, srvURL, agency, 32000)
}

// .
// .
func legFixtureBudget(t *testing.T, srvURL string, agency AgencyConfig, budget int) (*App, *store.Store, *cognitive.Executor) {
	t.Helper()
	st, err := store.New(filepath.Join(t.TempDir(), "aii.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	rm := ring.NewManager()
	_ = rm.SealSafePosture("test constitution")
	rm.Set(ring.Ring5, &ring.RingContent{Level: ring.Ring5, Content: "test floor"})
	reg := tools.NewRegistry(t.TempDir(), nil, tools.Timeouts{})
	cfg := &Config{
		SourcePath: filepath.Join(t.TempDir(), "config.json"),
		Prompt:     PromptConfig{MaxTokens: budget, MaxToolResultChars: 32000},
		Agency:     agency,
	}
	a := New(cfg)
	a.store = st
	a.toolReg = reg
	a.composer = prompt.New(rm, cfg.Prompt.MaxTokens)
	a.promptGate = prompt.NewGate(appRingSource{rm: rm}, cfg.Prompt.MaxTokens)
	a.llmSwap = newSwappableLLM(llm.New(&llm.ClientConfig{Endpoint: srvURL, Model: "fake-child", MaxOutputTokens: 64, Retries: -1}))
	a.engine = identity.NewEngine(st, nil, rm, toolDiscovererAdapter{reg})
	a.engine.SetAgencyLimits(2, 1, 20, agency.SubagentWallSeconds)
	ex := cognitive.NewExecutor(st)
	ex.SetHolds(a.fg)
	ex.RegisterHandler(&subagentHandler{a: a})
	a.engine.SetWorkWake(ex.Wake)
	a.queueWake = ex.Wake
	runCtx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	ex.Start(runCtx)
	t.Cleanup(ex.Stop)
	return a, st, ex
}

func spawnChild(t *testing.T, a *App, st *store.Store, goal string) string {
	t.Helper()
	req := identity.SubagentRequest{Goal: goal, Depth: 1, SessionID: "ws_leg", WallSeconds: 5, Context: "folded"}
	payload, _ := json.Marshal(req)
	if _, enq, err := st.EnqueueWorkWithSessionBelowLimit(&store.WorkItem{
		Kind: identity.SubagentWorkKind, Payload: string(payload), DedupKey: "ws_leg", Source: "identity", LeaseMs: 65000,
	}, 4, "ws_leg", store.SubagentDescription(goal)); err != nil || !enq {
		t.Fatalf("enqueue: %v %v", enq, err)
	}
	a.queueWake()
	return "ws_leg"
}

// .
// .
// .
func waitDelivered(t *testing.T, st *store.Store, id string) *store.WorkSession {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		var n int
		if err := st.DB().QueryRow(`SELECT COUNT(*) FROM outbox WHERE id = ?`, "subagent_"+id).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n > 0 {
			ws, err := st.WorkSessionByID(id)
			if err != nil {
				t.Fatal(err)
			}
			if ws == nil || ws.Status == "active" {
				t.Fatalf("notice written but the session is not delivered: %+v", ws)
			}
			return ws
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("the child never delivered")
	return nil
}

// .
// .
// .
func TestChildContinuesAtItsCeilingAndDeliversOnce(t *testing.T) {
	srv, _ := scriptedChild(t, 2)
	a, st, _ := legFixture(t, srv.URL, AgencyConfig{MaxToolRounds: 2, SubagentMaxToolRounds: 2, SubagentMaxToolCalls: 64, SubagentMaxLegs: 4, SubagentWallSeconds: 5})
	id := spawnChild(t, a, st, "count the beans in two legs")
	ws := waitDelivered(t, st, id)
	if !strings.Contains(ws.Result, "legs=2/4") {
		t.Fatalf("the header must name the chain: %q", ws.Result)
	}
	if strings.Contains(ws.Result, "UNFINISHED") {
		t.Fatalf("a chain that finished must not deliver unfinished: %q", ws.Result)
	}
	if !strings.Contains(ws.Result, "done in leg two") {
		t.Fatalf("the second leg's verdict must be the delivery: %q", ws.Result)
	}
	var notices int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM outbox WHERE id = ?`, "subagent_"+id).Scan(&notices); err != nil {
		t.Fatal(err)
	}
	if notices != 1 {
		t.Fatalf("one notice per child, got %d", notices)
	}
	var legs, calls int
	if err := st.DB().QueryRow(`SELECT legs, calls FROM subagent_metrics WHERE session_id = ?`, id).Scan(&legs, &calls); err != nil {
		t.Fatal(err)
	}
	if legs != 2 || calls != 2 {
		t.Fatalf("metric must sum the chain: legs=%d calls=%d", legs, calls)
	}
	var items int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM work_queue WHERE kind = ? AND dedup_key LIKE 'ws_leg#%'`, identity.SubagentWorkKind).Scan(&items); err != nil {
		t.Fatal(err)
	}
	if items != 1 {
		t.Fatalf("exactly one continuation item expected, got %d", items)
	}
}

// .
// .
func TestChildWithoutLegsDeliversUnfinishedAsBefore(t *testing.T) {
	off := false
	for name, agency := range map[string]AgencyConfig{
		"one leg": {MaxToolRounds: 2, SubagentMaxToolRounds: 2, SubagentMaxToolCalls: 64, SubagentMaxLegs: 1, SubagentWallSeconds: 5},
		"off":     {MaxToolRounds: 2, SubagentMaxToolRounds: 2, SubagentMaxToolCalls: 64, SubagentMaxLegs: 4, SubagentWallSeconds: 5, SubagentContinuation: &off},
	} {
		t.Run(name, func(t *testing.T) {
			srv, _ := scriptedChild(t, 2)
			a, st, _ := legFixture(t, srv.URL, agency)
			id := spawnChild(t, a, st, "count the beans")
			ws := waitDelivered(t, st, id)
			if !strings.Contains(ws.Result, "UNFINISHED") {
				t.Fatalf("expected the unfinished ending: %q", ws.Result)
			}
			var legs int
			if err := st.DB().QueryRow(`SELECT legs FROM subagent_metrics WHERE session_id = ?`, id).Scan(&legs); err != nil {
				t.Fatal(err)
			}
			if legs != 1 {
				t.Fatalf("a single run records one leg, got %d", legs)
			}
		})
	}
}

// .
// .
func TestChildDeclarationFeedsItsLegMeterOnly(t *testing.T) {
	a := New(&Config{SourcePath: filepath.Join(t.TempDir(), "config.json")})
	m, release := a.registerLegMeter("ws_x")
	defer release()
	a.noteSubagentWorkCall("ws_x", `{"action":"update","steps":7}`)
	if m.get() != 7 {
		t.Fatalf("leg meter = %d, want 7", m.get())
	}
	a.noteSubagentWorkCall("ws_other", `{"action":"update","steps":3}`)
	if m.get() != 7 {
		t.Fatal("another session's declaration reached this meter")
	}
	if a.turnPredicted != 0 {
		t.Fatal("a child's declaration reached the resident's meter")
	}
	if spawned, predicted, independent := parseWorkDeclaration(`{"action":"update","steps":4,"independent":2}`); spawned || predicted != 4 || independent != 2 {
		t.Fatalf("parse: %v %d %d", spawned, predicted, independent)
	}
}

// .
func TestContinuationPrefaceQuotesTheSession(t *testing.T) {
	ws := &store.WorkSession{Focus: "beans", NextMove: "count jar 3", Plan: "- jars 1..5", State: "two counted"}
	p := continuationPreface(2, 4, ws)
	for _, want := range []string{"leg 2 of 4", "SAME work session", "Next move: count jar 3", "- jars 1..5", "State: two counted", "Continue from the next move", "steps="} {
		if !strings.Contains(p, want) {
			t.Errorf("preface missing %q", want)
		}
	}
	if q := continuationPreface(3, 4, &store.WorkSession{}); !strings.Contains(q, "No next move was recorded") {
		t.Error("a session without a next move must be told to record one first")
	}
}

// .
// .
// .
// .
func deliveringChild(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		k := n.Add(1)
		w.Header().Set("Content-Type", "application/json")
		if k == 1 {
			_, _ = io.WriteString(w, `{"id":"c","model":"fake-child","choices":[{"index":0,"message":{"role":"assistant","content":"","tool_calls":[{"id":"call_1","type":"function","function":{"name":"work","arguments":"{\"action\":\"deliver\",\"result\":\"served: beans counted by the child\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":100,"completion_tokens":10,"total_tokens":110}}`)
			return
		}
		_, _ = io.WriteString(w, `{"id":"c","model":"fake-child","choices":[{"index":0,"message":{"role":"assistant","content":"still going","tool_calls":[{"id":"call_`+fmt.Sprint(k)+`","type":"function","function":{"name":"ls","arguments":"{\"path\":\".\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":100,"completion_tokens":10,"total_tokens":110}}`)
	}))
	t.Cleanup(srv.Close)
	return srv, &n
}

// .
// .
// .
// .
// .
func TestChildThatDeliversEndsItsRunAndIsNotContinued(t *testing.T) {
	srv, n := deliveringChild(t)
	a, st, _ := legFixture(t, srv.URL, AgencyConfig{MaxToolRounds: 6, SubagentMaxToolRounds: 6, SubagentMaxToolCalls: 64, SubagentMaxLegs: 4, SubagentWallSeconds: 5})
	id := spawnChild(t, a, st, "count the beans and deliver")
	ws := waitDelivered(t, st, id)
	if ws.Result != "served: beans counted by the child" {
		t.Fatalf("the child's own delivery must stand: %q", ws.Result)
	}
	if got := n.Load(); got > 2 {
		t.Fatalf("the run must end at the delivery (one tool round, one closing reply); the model was called %d times", got)
	}
	var items int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM work_queue WHERE kind = ? AND dedup_key LIKE 'ws_leg#%'`, identity.SubagentWorkKind).Scan(&items); err != nil {
		t.Fatal(err)
	}
	if items != 0 {
		t.Fatalf("a delivered child must not be continued, got %d continuation item(s)", items)
	}
	var legs, calls int
	if err := st.DB().QueryRow(`SELECT legs, calls FROM subagent_metrics WHERE session_id = ?`, id).Scan(&legs, &calls); err != nil {
		t.Fatal(err)
	}
	if legs != 1 || calls != 1 {
		t.Fatalf("the cost row is written for a child that delivered itself: legs=%d calls=%d", legs, calls)
	}
}

// .
// .
// .
func TestLegAgainstADeliveredSessionIsNotRun(t *testing.T) {
	srv, n := deliveringChild(t)
	a, st, _ := legFixture(t, srv.URL, AgencyConfig{MaxToolRounds: 6, SubagentMaxToolRounds: 6, SubagentMaxToolCalls: 64, SubagentMaxLegs: 4, SubagentWallSeconds: 5})
	id := spawnChild(t, a, st, "count the beans and deliver")
	waitDelivered(t, st, id)
	before := n.Load()
	req := identity.SubagentRequest{Goal: "count the beans and deliver", Depth: 1, SessionID: id, WallSeconds: 5, Context: "folded", Leg: 2}
	payload, _ := json.Marshal(req)
	if _, err := st.EnqueueWork(&store.WorkItem{Kind: identity.SubagentWorkKind, Payload: string(payload), DedupKey: id + "#2", Source: "identity", LeaseMs: 65000}); err != nil {
		t.Fatal(err)
	}
	a.queueWake()
	deadline := time.Now().Add(20 * time.Second)
	for {
		var state string
		if err := st.DB().QueryRow(`SELECT state FROM work_queue WHERE dedup_key = ?`, id+"#2").Scan(&state); err != nil {
			t.Fatal(err)
		}
		if state == "DONE" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the leg item never completed (state %s)", state)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if got := n.Load(); got != before {
		t.Fatalf("a leg against a delivered session must not call the model: %d call(s) more", got-before)
	}
	var rows int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM subagent_metrics WHERE session_id = ?`, id).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Fatalf("the skipped leg must not write a second cost row, got %d", rows)
	}
}

// .
// .
// .
// .
func TestRuntimeDeliveriesBeginWithTheVerdict(t *testing.T) {
	t.Run("exhausted", func(t *testing.T) {
		srv, _ := scriptedChild(t, 2)
		a, st, _ := legFixture(t, srv.URL, AgencyConfig{MaxToolRounds: 2, SubagentMaxToolRounds: 2, SubagentMaxToolCalls: 64, SubagentMaxLegs: 1, SubagentWallSeconds: 5})
		id := spawnChild(t, a, st, "count the beans")
		ws := waitDelivered(t, st, id)
		if !strings.HasPrefix(ws.Result, "partial: UNFINISHED") {
			t.Fatalf("an exhausted child is partial, verdict first: %q", ws.Result)
		}
		lines := strings.SplitN(ws.Result, "\n", 3)
		if len(lines) < 2 || !strings.HasPrefix(lines[1], "[sub-agent role=") {
			t.Fatalf("the cost row is the second line: %q", ws.Result)
		}
	})
	t.Run("final message without a verdict", func(t *testing.T) {
		srv, _ := scriptedChild(t, 1)
		a, st, _ := legFixture(t, srv.URL, AgencyConfig{MaxToolRounds: 4, SubagentMaxToolRounds: 4, SubagentMaxToolCalls: 64, SubagentMaxLegs: 1, SubagentWallSeconds: 5})
		id := spawnChild(t, a, st, "count the beans")
		ws := waitDelivered(t, st, id)
		if !strings.HasPrefix(ws.Result, "partial: ended without a stated verdict") || !strings.Contains(ws.Result, "done in leg two") {
			t.Fatalf("a final message without a verdict is delivered as partial, with the message: %q", ws.Result)
		}
	})
}
