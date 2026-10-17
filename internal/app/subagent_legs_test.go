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

// scriptedChild answers the first `toolRounds` requests with one tool
// call each and every later request with text — so leg one ends at a
// two-round ceiling and leg two finishes naturally.
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

// legFixtureBudget is legFixture with the prompt budget the child is
// composed and run under, for the tests that need context pressure.
func legFixtureBudget(t *testing.T, srvURL string, agency AgencyConfig, budget int) (*App, *store.Store, *cognitive.Executor) {
	t.Helper()
	st, err := store.NewForTest(filepath.Join(t.TempDir(), "aii.db"))
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
	ex := cognitive.NewExecutor(st)
	ex.SetHolds(a.fg)
	ex.RegisterHandler(&subagentHandler{a: a})
	a.engine = identity.NewEngine(st, nil, rm, toolDiscovererAdapter{reg}, identity.Ports{WorkWake: ex.Wake})
	a.engine.SetAgencyLimits(2, 1, 20, agency.SubagentWallSeconds)
	a.queueWake = ex.Wake
	runCtx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	ex.Start(runCtx)
	t.Cleanup(ex.Stop)
	return a, st, ex
}

func spawnChild(t *testing.T, a *App, st *store.Store, goal string, parent ...string) string {
	t.Helper()
	req := identity.SubagentRequest{Goal: goal, Depth: 1, SessionID: "ws_leg", WallSeconds: 5, Context: "folded"}
	if len(parent) != 0 {
		req.ParentSessionID = parent[0]
	}
	payload, _ := json.Marshal(req)
	if _, enq, err := st.EnqueueWorkWithSessionBelowLimit(&store.WorkItem{
		Kind: identity.SubagentWorkKind, Payload: string(payload), DedupKey: "ws_leg", Source: "identity", LeaseMs: 65000,
	}, 4, "ws_leg", store.SubagentDescription(goal)); err != nil || !enq {
		t.Fatalf("enqueue: %v %v", enq, err)
	}
	a.queueWake()
	return "ws_leg"
}

// waitDelivered waits for the operator's notice — the LAST write of a
// run, after delivery and the metric row — so every assertion that
// follows reads a finished run rather than racing its tail.
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

// Leg one ends at the two-round ceiling; leg two is enqueued against
// the same session and finishes; the chain delivers ONCE with a header
// that names the legs, one notice, one metric row summing both legs.
func TestChildContinuesAtItsCeilingAndDeliversOnce(t *testing.T) {
	srv, _ := scriptedChild(t, 2)
	a, st, _ := legFixture(t, srv.URL, AgencyConfig{MaxToolRounds: 2, SubagentMaxToolRounds: 2, SubagentMaxToolCalls: 64, SubagentMaxLegs: 4, SubagentWallSeconds: 5})
	if err := st.StartWorkSession(t.Context(), "ws_parent", "parent work"); err != nil {
		t.Fatal(err)
	}
	id := spawnChild(t, a, st, "count the beans in two legs", "ws_parent")
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
	var emitted, coverage int
	if err := st.DB().QueryRow(`SELECT emitted_calls, emission_coverage_complete FROM subagent_metrics WHERE session_id=?`, id).Scan(&emitted, &coverage); err != nil || emitted != 2 || coverage != 1 {
		t.Fatalf("real continuation lost emission coverage: %d/%d %v", emitted, coverage, err)
	}
	var items int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM work_queue WHERE kind = ? AND dedup_key LIKE 'ws_leg#%'`, identity.SubagentWorkKind).Scan(&items); err != nil {
		t.Fatal(err)
	}
	if items != 1 {
		t.Fatalf("exactly one continuation item expected, got %d", items)
	}
	var parents int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM work_queue WHERE kind=? AND json_extract(payload,'$.ws_id')=? AND json_extract(payload,'$.parent_work_session_id')='ws_parent'`, identity.SubagentWorkKind, id).Scan(&parents); err != nil {
		t.Fatal(err)
	}
	if parents != 2 {
		t.Fatalf("both actual continuation legs must retain the parent; got %d", parents)
	}
}

// With one leg allowed, or continuation off, the first bounded ending
// delivers UNFINISHED as before — the cliff is the operator's choice.
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

// The child's declaration feeds its own meter; the resident's meter
// never sees it.
func TestChildDeclarationFeedsItsLegMeterOnly(t *testing.T) {
	a := New(&Config{SourcePath: filepath.Join(t.TempDir(), "config.json")})
	m, release := a.registerLegMeter("ws_x")
	defer release()
	a.noteSubagentWorkCall("ws_x", decodeArgs(`{"action":"update","steps":7}`), 6)
	if remaining, ordinal := m.get(); remaining != 7 || ordinal != 6 {
		t.Fatalf("leg meter = %d at %d, want 7 at 6", remaining, ordinal)
	}
	a.noteSubagentWorkCall("ws_other", decodeArgs(`{"action":"update","steps":3}`), 8)
	if remaining, ordinal := m.get(); remaining != 7 || ordinal != 6 {
		t.Fatal("another session's declaration reached this meter")
	}
	if a.turn.turnPredicted != 0 {
		t.Fatal("a child's declaration reached the resident's meter")
	}
	// An older completion cannot move the declaration window backward.
	a.noteSubagentWorkCall("ws_x", decodeArgs(`{"action":"update","steps":2}`), 5)
	if remaining, ordinal := m.get(); remaining != 7 || ordinal != 6 {
		t.Fatalf("older completion replaced the pair: %d at %d", remaining, ordinal)
	}
	// Even the same estimate at a later position describes new remaining work.
	a.noteSubagentWorkCall("ws_x", decodeArgs(`{"action":"update","steps":7}`), 9)
	if remaining, ordinal := m.get(); remaining != 7 || ordinal != 9 {
		t.Fatalf("revision did not move its window: %d at %d", remaining, ordinal)
	}
	if spawned, predicted, independent := workDeclaration(decodeArgs(`{"action":"update","steps":4,"independent":2}`)); spawned || predicted != 4 || independent != 2 {
		t.Fatalf("parse: %v %d %d", spawned, predicted, independent)
	}
}

// The preface quotes the session and says what to do without a next move.
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

// deliveringChild: round one calls `work deliver`; every later round
// offers another tool call with a line of text, the way a real child
// keeps going — so the run ends at the delivery only if the delivery
// ends it.
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

// A continuation leg that finds its session already delivered does not
// run: the queue can hold a leg enqueued before the delivery landed, or
// carried across a restart.
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

// recoverAfterAHardKill leaves a leg's queue row as a hard kill leaves it
// — CLAIMED, its lease long expired, never completed — and drives the
// executor until the real sweep and claim have recovered it and it is DONE
// again.
func recoverAfterAHardKill(t *testing.T, a *App, st *store.Store, dedup string) {
	t.Helper()
	if _, err := st.DB().Exec(`UPDATE work_queue SET state = 'CLAIMED', claimed_at = 1, done_at = 0 WHERE dedup_key = ?`, dedup); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(20 * time.Second)
	for {
		a.queueWake()
		time.Sleep(100 * time.Millisecond)
		var state string
		var retries int
		if err := st.DB().QueryRow(`SELECT state, retry_count FROM work_queue WHERE dedup_key = ?`, dedup).Scan(&state, &retries); err != nil {
			t.Fatal(err)
		}
		if state == "DONE" && retries > 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the recovered item never completed (state %s, retries %d)", state, retries)
		}
	}
}

func TestARecoveredFirstLegAgainstADeliveredSessionIsNotRun(t *testing.T) {
	srv, n := deliveringChild(t)
	a, st, _ := legFixture(t, srv.URL, AgencyConfig{MaxToolRounds: 6, SubagentMaxToolRounds: 6, SubagentMaxToolCalls: 64, SubagentMaxLegs: 4, SubagentWallSeconds: 5})
	id := spawnChild(t, a, st, "count the beans and deliver")
	waitDelivered(t, st, id)
	before := n.Load()
	recoverAfterAHardKill(t, a, st, id)
	if got := n.Load(); got != before {
		t.Fatalf("a recovered first leg against a delivered session called the model %d more time(s)", got-before)
	}
	var rows int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM subagent_metrics WHERE session_id = ?`, id).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Fatalf("the refused leg must not write a second cost row, got %d", rows)
	}
	if ws, _ := st.WorkSessionByID(id); ws == nil || ws.Result != "served: beans counted by the child" {
		t.Fatalf("the child's own delivery must stand: %+v", ws)
	}
}

// Its control half: a first leg whose session holds the failure-before-
// start marker is run, because nothing ran, and its result replaces the
// marker. The guard reads delivered as a real result, never the marker.
func TestAFirstLegThatFailedBeforeItStartedIsRunAgain(t *testing.T) {
	srv, n := deliveringChild(t)
	a, st, _ := legFixture(t, srv.URL, AgencyConfig{MaxToolRounds: 6, SubagentMaxToolRounds: 6, SubagentMaxToolCalls: 64, SubagentMaxLegs: 4, SubagentWallSeconds: 5})
	const id = "ws_prestart"
	const goal = "count the beans after a failed start"
	if err := st.StartWorkSession(t.Context(), id, store.SubagentDescription(goal)); err != nil {
		t.Fatal(err)
	}
	if err := st.DeliverWorkSession(t.Context(), id, store.PreStartFailure("compose: the rig's failure"), store.EvidenceNotRun, ""); err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(identity.SubagentRequest{Goal: goal, Depth: 1, SessionID: id, WallSeconds: 5, Context: "folded"})
	if _, err := st.EnqueueWork(&store.WorkItem{Kind: identity.SubagentWorkKind, Payload: string(payload), DedupKey: id, Source: "identity", LeaseMs: 65000}); err != nil {
		t.Fatal(err)
	}
	a.queueWake()
	ws := waitDelivered(t, st, id)
	if n.Load() == 0 || ws.Result != "served: beans counted by the child" {
		t.Fatalf("a first leg that failed before it started must run and replace the marker: calls=%d result=%q", n.Load(), ws.Result)
	}
}

// killedLeg makes a sub-agent session and its first leg's queue row as a
// hard kill leaves them: CLAIMED, its lease long expired, never completed.
// The leg's run had begun when started says so, by the adapter's own
// write (StartWorkSessionLeg).
func killedLeg(t *testing.T, st *store.Store, id, goal string, started bool) {
	t.Helper()
	if err := st.StartWorkSession(t.Context(), id, store.SubagentDescription(goal)); err != nil {
		t.Fatal(err)
	}
	if started {
		if err := st.StartWorkSessionLeg(id, 1); err != nil {
			t.Fatal(err)
		}
	}
	payload, _ := json.Marshal(identity.SubagentRequest{Goal: goal, Depth: 1, SessionID: id, WallSeconds: 5, Context: "folded"})
	if _, err := st.EnqueueWork(&store.WorkItem{Kind: identity.SubagentWorkKind, Payload: string(payload), DedupKey: id, Source: "identity", State: "CLAIMED", LeaseMs: 65000}); err != nil {
		t.Fatal(err)
	}
}

// THE START IS RECORDED BEFORE THE RUN'S FIRST CALL. The marker the
// no-replay law reads must be durable before anything the run does can
// act: the model's first request already finds it.
func TestALegsStartIsRecordedBeforeItsFirstModelCall(t *testing.T) {
	var held atomic.Pointer[store.Store]
	var seen atomic.Int32
	seen.Store(-1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if st := held.Load(); st != nil {
			leg, _ := st.WorkSessionStartedLeg("ws_leg")
			seen.CompareAndSwap(-1, int32(leg))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"c","model":"fake-child","choices":[{"index":0,"message":{"role":"assistant","content":"served: counted"},"finish_reason":"stop"}],"usage":{"prompt_tokens":100,"completion_tokens":5,"total_tokens":105}}`)
	}))
	t.Cleanup(srv.Close)
	a, st, _ := legFixture(t, srv.URL, AgencyConfig{MaxToolRounds: 6, SubagentMaxToolRounds: 6, SubagentMaxToolCalls: 64, SubagentMaxLegs: 4, SubagentWallSeconds: 5})
	held.Store(st)
	id := spawnChild(t, a, st, "count the beans")
	waitDelivered(t, st, id)
	if got := seen.Load(); got != 1 {
		t.Fatalf("the model's first request found started leg %d, want 1: the start was not recorded before the run began", got)
	}
}

func TestALegThatHadBegunIsDeliveredAsEffectUnknownAndNotRunAgain(t *testing.T) {
	srv, n := deliveringChild(t)
	a, st, _ := legFixture(t, srv.URL, AgencyConfig{MaxToolRounds: 6, SubagentMaxToolRounds: 6, SubagentMaxToolCalls: 64, SubagentMaxLegs: 4, SubagentWallSeconds: 5})
	const id = "ws_killed"
	killedLeg(t, st, id, "count the beans before the power fails", true)
	focus, next := "the beans", "count jar 3"
	if err := st.UpdateWorkPlan(t.Context(), id, &focus, &next, nil, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	recoverAfterAHardKill(t, a, st, id)
	ws := waitDelivered(t, st, id)
	if got := n.Load(); got != 0 {
		t.Fatalf("a leg that had begun was run again from its goal: the model was called %d time(s); result %q", got, ws.Result)
	}
	if ws.Evidence != store.EvidenceExternalUnknown || !strings.HasPrefix(ws.Result, "unserved: ") {
		t.Fatalf("an interrupted leg is delivered unserved, its effects unknown: [%s] %q", ws.Evidence, ws.Result)
	}
	for _, want := range []string{"interrupted by a runtime restart", "leg 1 of 4", "Focus: the beans", "Next move: count jar 3"} {
		if !strings.Contains(ws.Result, want) {
			t.Errorf("the delivery must carry %q: %q", want, ws.Result)
		}
	}
	var failed int
	if err := st.DB().QueryRow(`SELECT failed FROM subagent_metrics WHERE session_id = ?`, id).Scan(&failed); err != nil || failed != 1 {
		t.Fatalf("the interrupted leg's cost row is written as failed: %d %v", failed, err)
	}
}

// And a leg recovered before its start was recorded still runs: nothing of
// it can have acted.
func TestALegRecoveredBeforeItsStartStillRuns(t *testing.T) {
	srv, n := deliveringChild(t)
	a, st, _ := legFixture(t, srv.URL, AgencyConfig{MaxToolRounds: 6, SubagentMaxToolRounds: 6, SubagentMaxToolCalls: 64, SubagentMaxLegs: 4, SubagentWallSeconds: 5})
	const id = "ws_unstarted"
	killedLeg(t, st, id, "count the beans after a kill before the start", false)
	recoverAfterAHardKill(t, a, st, id)
	ws := waitDelivered(t, st, id)
	if n.Load() == 0 || ws.Result != "served: beans counted by the child" || ws.Evidence == store.EvidenceExternalUnknown {
		t.Fatalf("a leg recovered before its start must run: calls=%d [%s] %q", n.Load(), ws.Evidence, ws.Result)
	}
}

// A leg that had begun and handed over to its next leg owes nothing when its
// item comes back: the kill fell between enqueuing the next leg and
// completing this one. It completes quietly, and the next leg runs.
func TestALegThatHandedOverOwesNothingWhenItsItemComesBack(t *testing.T) {
	srv, n := deliveringChild(t)
	a, st, _ := legFixture(t, srv.URL, AgencyConfig{MaxToolRounds: 6, SubagentMaxToolRounds: 6, SubagentMaxToolCalls: 64, SubagentMaxLegs: 4, SubagentWallSeconds: 5})
	const id = "ws_handed"
	const goal = "count the beans in two legs"
	killedLeg(t, st, id, goal, true)
	payload, _ := json.Marshal(identity.SubagentRequest{Goal: goal, Depth: 1, SessionID: id, WallSeconds: 5, Context: "folded", Leg: 2})
	if _, err := st.EnqueueWork(&store.WorkItem{Kind: identity.SubagentWorkKind, Payload: string(payload), DedupKey: id + "#2", Source: "identity", LeaseMs: 65000,
		Scheduled: time.Now().Add(time.Hour).UnixMilli()}); err != nil {
		t.Fatal(err)
	}
	recoverAfterAHardKill(t, a, st, id)
	if ws, _ := st.WorkSessionByID(id); n.Load() != 0 || ws == nil || ws.Status != "active" {
		t.Fatalf("a leg that handed over must complete quietly: calls=%d session=%+v", n.Load(), ws)
	}
	if _, err := st.DB().Exec(`UPDATE work_queue SET scheduled_ms = 0 WHERE dedup_key = ?`, id+"#2"); err != nil {
		t.Fatal(err)
	}
	a.queueWake()
	if ws := waitDelivered(t, st, id); ws.Result != "served: beans counted by the child" {
		t.Fatalf("the next leg runs and delivers: %q", ws.Result)
	}
}

// boundedChild ends leg one at a budget that is neither a round nor a call
// ceiling: its first reply is one tool round (first, the round's reply),
// its second the wrap-up the loop asks for at the bound, and every later
// leg finishes.
func boundedChild(t *testing.T, first string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		k := n.Add(1)
		w.Header().Set("Content-Type", "application/json")
		switch k {
		case 1:
			_, _ = io.WriteString(w, first)
		case 2:
			_, _ = io.WriteString(w, `{"id":"c","model":"fake-child","choices":[{"index":0,"message":{"role":"assistant","content":"wrapping up at the bound"},"finish_reason":"stop"}],"usage":{"prompt_tokens":100,"completion_tokens":5,"total_tokens":105}}`)
		default:
			_, _ = io.WriteString(w, `{"id":"c","model":"fake-child","choices":[{"index":0,"message":{"role":"assistant","content":"VERDICT: done in leg two"},"finish_reason":"stop"}],"usage":{"prompt_tokens":100,"completion_tokens":5,"total_tokens":105}}`)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &n
}

// fenceRound is a tool round whose reported usage is tokens: past a turn
// token fence below it.
func fenceRound(tokens int) string {
	return fmt.Sprintf(`{"id":"c","model":"fake-child","choices":[{"index":0,"message":{"role":"assistant","content":"","tool_calls":[{"id":"call_1","type":"function","function":{"name":"ls","arguments":"{\"path\":\".\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":%d,"completion_tokens":10,"total_tokens":%d}}`, tokens-10, tokens)
}

// pressureRound is a tool round whose call carries an argument the child's
// context cannot hold beside the tools' schemas, and can without them: run
// under pressureBudget, the next request does not fit, and the wrap-up
// does. Measured here: the first request needs about 11,900 tokens, about
// 4,200 of them the schemas; 32,000 characters (about 8,000 tokens) leave
// some 4,000 tokens of margin on each side.
const pressureBudget = 20000

func pressureRound() string {
	return `{"id":"c","model":"fake-child","choices":[{"index":0,"message":{"role":"assistant","content":"","tool_calls":[{"id":"call_1","type":"function","function":{"name":"ls","arguments":"{\"path\":\".\",\"note\":\"` + strings.Repeat("p", 32000) + `\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":100,"completion_tokens":10,"total_tokens":110}}`
}

func TestAChildEndedByItsTokenFenceOrItsContextContinues(t *testing.T) {
	agency := AgencyConfig{MaxToolRounds: 6, SubagentMaxToolRounds: 6, SubagentMaxToolCalls: 64, SubagentMaxLegs: 4, SubagentWallSeconds: 5}
	for name, c := range map[string]struct {
		first  string
		budget int
	}{
		"token fence": {fenceRound(700_010), 32000},
		"context":     {pressureRound(), pressureBudget},
	} {
		t.Run(name, func(t *testing.T) {
			srv, _ := boundedChild(t, c.first)
			a, st, _ := legFixtureBudget(t, srv.URL, agency, c.budget)
			id := spawnChild(t, a, st, "count the beans past the bound")
			ws := waitDelivered(t, st, id)
			var items int
			if err := st.DB().QueryRow(`SELECT COUNT(*) FROM work_queue WHERE kind = ? AND dedup_key = ?`, identity.SubagentWorkKind, id+"#2").Scan(&items); err != nil {
				t.Fatal(err)
			}
			if items != 1 || !strings.Contains(ws.Result, "done in leg two") || !strings.Contains(ws.Result, "legs=2/4") {
				t.Fatalf("a child ended by its %s with legs remaining must continue: leg-2 items %d, result %q", name, items, ws.Result)
			}
		})
	}
}

// And one that cannot continue — one leg allowed, or continuation off — is
// delivered unfinished, its verdict line saying which budget ended it and
// that nothing continues it, never "served:" or exhausted=false under the
// loop's promise that the work continues.
func TestAChildEndedByABudgetItCannotContinueIsDeliveredUnfinished(t *testing.T) {
	off := false
	for _, c := range []struct {
		name, first, says string
		budget            int
	}{
		{"token fence", fenceRound(700_010), "spent its token budget", 32000},
		{"context", pressureRound(), "context filled", pressureBudget},
	} {
		for legs, agency := range map[string]AgencyConfig{
			"one leg": {MaxToolRounds: 6, SubagentMaxToolRounds: 6, SubagentMaxToolCalls: 64, SubagentMaxLegs: 1, SubagentWallSeconds: 5},
			"off":     {MaxToolRounds: 6, SubagentMaxToolRounds: 6, SubagentMaxToolCalls: 64, SubagentMaxLegs: 4, SubagentWallSeconds: 5, SubagentContinuation: &off},
		} {
			t.Run(c.name+"/"+legs, func(t *testing.T) {
				srv, _ := boundedChild(t, c.first)
				a, st, _ := legFixtureBudget(t, srv.URL, agency, c.budget)
				id := spawnChild(t, a, st, "count the beans past the bound")
				ws := waitDelivered(t, st, id)
				lines := strings.SplitN(ws.Result, "\n", 3)
				if len(lines) < 2 || !strings.HasPrefix(lines[0], "partial: UNFINISHED") || !strings.Contains(lines[0], c.says) || !strings.Contains(lines[0], "nothing continues it") {
					t.Fatalf("the verdict line must say unfinished, by what, and that nothing continues it: %q", ws.Result)
				}
				if !strings.Contains(lines[1], "exhausted=true") {
					t.Fatalf("the cost row must say exhausted: %q", lines[1])
				}
				var items int
				if err := st.DB().QueryRow(`SELECT COUNT(*) FROM work_queue WHERE kind = ? AND dedup_key LIKE ?`, identity.SubagentWorkKind, id+"#%").Scan(&items); err != nil || items != 0 {
					t.Fatalf("nothing may be enqueued: %d %v", items, err)
				}
			})
		}
	}
}

func TestAChildIsFencedByTheConfiguredTurnTokenBudget(t *testing.T) {
	srv, _ := boundedChild(t, fenceRound(1500))
	a, st, _ := legFixture(t, srv.URL, AgencyConfig{MaxToolRounds: 6, SubagentMaxToolRounds: 6, SubagentMaxToolCalls: 64, SubagentMaxLegs: 4, SubagentWallSeconds: 5, TurnTokenBudget: 1000})
	id := spawnChild(t, a, st, "count the beans under a small fence")
	ws := waitDelivered(t, st, id)
	if !strings.Contains(ws.Result, "done in leg two") || !strings.Contains(ws.Result, "legs=2/4") {
		t.Fatalf("a child must be fenced at the configured turn token budget: %q", ws.Result)
	}
}

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
