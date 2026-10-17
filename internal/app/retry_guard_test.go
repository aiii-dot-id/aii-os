package app

import (
	"context"
	"encoding/json"
	"errors"
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
	storerows "github.com/aiii-dot-id/aii-os/internal/store/rows"
	"github.com/aiii-dot-id/aii-os/internal/tools"
)

func TestFailedSubagentRunIsDeliveredOnceAndNeverRetried(t *testing.T) {
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError) // the run fails after it began
	}))
	defer fake.Close()

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
		Endpoint: fake.URL, Model: "fake-subagent", MaxOutputTokens: 64, Retries: -1,
	}))
	ex := cognitive.NewExecutor(st)
	ex.SetHolds(a.fg)
	ex.RegisterHandler(&subagentHandler{a: a})
	a.engine = identity.NewEngine(st, nil, rm, toolDiscovererAdapter{reg}, identity.Ports{WorkWake: ex.Wake})
	a.engine.SetAgencyLimits(2, 1, 20, cfg.Agency.SubagentWallSeconds)

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

	// The item must settle DONE on attempt one; PENDING-with-backoff is
	// the replay this law forbids, so poll for a terminal state.
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

	runs, _, _, failedRuns, err := st.SubagentStats(time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if runs != 1 || failedRuns != 1 {
		t.Fatalf("subagent_metrics holds (runs=%d, failed=%d), want (1, 1) — the child run left no cost trace", runs, failedRuns)
	}
}

// The store-level half of the same law: a session already delivered
// with a REAL result refuses replacement; the one lawful overwrite is
// a retry's outcome replacing the pre-execution failure marker.
func TestDeliveryRefusesToOverwriteARealResult(t *testing.T) {
	st, err := store.NewForTest(filepath.Join(t.TempDir(), "aii.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	if err := st.StartWorkSession(t.Context(), "ws_law", "the delivery law"); err != nil {
		t.Fatal(err)
	}
	if err := st.DeliverWorkSession(t.Context(), "ws_law", "the real outcome", "", ""); err != nil {
		t.Fatalf("first delivery must land: %v", err)
	}
	if err := st.DeliverWorkSession(t.Context(), "ws_law", "a replayed outcome", "", ""); err == nil {
		t.Fatal("a second delivery over a real result landed — trajectory replay reached the record")
	}
	var got string
	if err := st.DB().QueryRow(`SELECT result FROM work_sessions WHERE id='ws_law'`).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != "the real outcome" {
		t.Fatalf("result = %q, the refusal must leave the first outcome untouched", got)
	}

	if err := st.StartWorkSession(t.Context(), "ws_early", "pre-exec retry path"); err != nil {
		t.Fatal(err)
	}
	if err := st.DeliverWorkSession(t.Context(), "ws_early", "unserved: failed before start: compose blew up", "", ""); err != nil {
		t.Fatal(err)
	}
	if err := st.DeliverWorkSession(t.Context(), "ws_early", "the retry's real outcome", "", ""); err != nil {
		t.Fatalf("replacing the pre-execution marker is the lawful path and must stay open: %v", err)
	}
}

func TestWorkStatusNamesDeliveredUnharvestedWork(t *testing.T) {
	st, err := store.NewForTest(filepath.Join(t.TempDir(), "aii.db"))
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
	a.engine = identity.NewEngine(st, nil, rm, toolDiscovererAdapter{reg}, identity.Ports{})

	if _, enq, err := st.EnqueueWorkWithSessionBelowLimit(&store.WorkItem{Kind: identity.SubagentWorkKind, Payload: "{}", DedupKey: "ws_stat", Source: "identity", LeaseMs: 1000},
		4, "ws_stat", store.SubagentDescription("survey the logs")); err != nil || !enq {
		t.Fatalf("enqueue: enq=%v err=%v", enq, err)
	}
	if err := st.DeliverWorkSession(t.Context(), "ws_stat", "found three things", "", ""); err != nil {
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

func TestSubagentEventsCarryTheActorLabel(t *testing.T) {
	a := New(&Config{SourcePath: filepath.Join(t.TempDir(), "config.json")})
	var gotArgs []string
	a.turn.toolEmit = func(kind, name, args string) { gotArgs = append(gotArgs, args) }

	appEmitter{a: a, actor: "ws_label"}.EmitToolEvent("call", "read", `{"f":1}`)
	appEmitter{a: a}.EmitToolEvent("call", "read", `{"f":2}`)

	if len(gotArgs) != 2 || !strings.HasPrefix(gotArgs[0], "[ws_label] ") {
		t.Fatalf("sub-agent event not labeled: %v", gotArgs)
	}
	if strings.HasPrefix(gotArgs[1], "[") {
		t.Fatalf("the resident's own event must stay unlabeled: %v", gotArgs)
	}
}

// The executor seam sets EndTurn for `work yield` and for nothing
// else — the loop's chosen-ending signal comes from typed observation,
// never from prose.
func TestWorkYieldSetsEndTurnAtTheExecutorSeam(t *testing.T) {
	st, err := store.NewForTest(filepath.Join(t.TempDir(), "aii.db"))
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
	a.engine = identity.NewEngine(st, nil, rm, toolDiscovererAdapter{reg}, identity.Ports{})

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

// Every compose renders running workers from the rows, not from the
// resident's own last sentence about them.
func TestComposeRendersRunningWorkerTruth(t *testing.T) {
	st, err := store.NewForTest(filepath.Join(t.TempDir(), "aii.db"))
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

	state, err := a.buildTurnFacts(t.Context(), false)
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

func TestSubagentCannotYieldTheTurn(t *testing.T) {
	st, err := store.NewForTest(filepath.Join(t.TempDir(), "aii.db"))
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
	a.engine = identity.NewEngine(st, nil, rm, toolDiscovererAdapter{reg}, identity.Ports{})

	var call llm.ToolCall
	call.Type = "function"
	call.Function.Name = "work"
	call.Function.Arguments = `{"action":"yield"}`

	// The resident's own path still yields.
	if obs := a.executeToolCall(t.Context(), call); !obs.EndTurn || obs.Failed {
		t.Fatalf("the resident lost its yield: Failed=%v EndTurn=%v", obs.Failed, obs.EndTurn)
	}

	// The same call inside a sub-agent run is refused, and the turn
	// does NOT end — depth is engine-stamped from ctx, never claimed.
	subCtx := context.WithValue(t.Context(), identity.SubagentDepth{}, 1)
	obs := a.executeToolCall(subCtx, call)
	if obs.EndTurn {
		t.Fatal("a sub-agent yielded the turn — its run would end early and deliver a fragment")
	}
	if !obs.Failed || !strings.Contains(obs.Text, "deliverable") {
		t.Fatalf("the refusal does not name the right move: Failed=%v text=%q", obs.Failed, obs.Text)
	}
}

// A run that fails before it starts delivers the host's pre-start marker,
// not_run, whatever ended its context, and returns the failure so the queue
// retries the item; a delivery the store refuses joins the returned error.
// Both of RunWork's pre-start failures (the request estimate, the compose)
// go through failBeforeStart.
func TestAFailureBeforeStartDeliversTheMarker(t *testing.T) {
	st, err := store.NewForTest(filepath.Join(t.TempDir(), "aii.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	a := New(&Config{SourcePath: filepath.Join(t.TempDir(), "config.json")})
	a.store = st
	h := &subagentHandler{a}
	for _, id := range []string{"ws_pre", "ws_real"} {
		queueChild(t, st, id)
		if err := st.StartWorkSession(t.Context(), id, store.SubagentDescription("count the beans")); err != nil {
			t.Fatal(err)
		}
	}

	ended, cancel := context.WithCancel(t.Context())
	cancel()
	retried := &store.WorkItem{RetryCount: 0, MaxRetries: 3} // an attempt the queue follows with another
	if err := h.failBeforeStart(ended, retried, identity.SubagentRequest{SessionID: "ws_pre"}, 1, 4, "compose", errors.New("blew up")); err == nil || err.Error() != "subagent compose: blew up" {
		t.Fatalf("failure = %v, want the phase and its cause alone", err)
	}
	ws, err := st.WorkSessionByID("ws_pre")
	if err != nil || ws == nil || ws.Status != "delivered" || ws.Result != "unserved: failed before start: blew up" || ws.Evidence != store.EvidenceNotRun {
		t.Fatalf("the marker did not land under an ended context: %+v %v", ws, err)
	}

	if err := st.DeliverWorkSession(t.Context(), "ws_real", "served: real", store.EvidenceWorkerReportOnly, ""); err != nil {
		t.Fatal(err)
	}
	err = h.failBeforeStart(t.Context(), retried, identity.SubagentRequest{SessionID: "ws_real"}, 1, 4, "request estimate", errors.New("no estimate"))
	if err == nil || !strings.HasPrefix(err.Error(), "subagent request estimate: no estimate\n") || !strings.Contains(err.Error(), "record subagent start failure: delivery refused") {
		t.Fatalf("a refused delivery is not joined to the failure: %v", err)
	}
	if ws, err := st.WorkSessionByID("ws_real"); err != nil || ws == nil || ws.Result != "served: real" {
		t.Fatalf("the real result was replaced: %+v %v", ws, err)
	}
}

// THE LAST FAILURE BEFORE START CLOSES THE CHILD, AND SAYS WHAT IS TRUE OF
// ITS LEG. The attempt that spends the retries left the item FAILED and the
// child never closed: no cost row, no notice to the operator, no wake for the
// parent that had yielded for this delivery, and a fleet still counted whole.
// It is closed as every finished child is.
//
// A first leg: while the queue holds another attempt the marker is all a
// failure before start leaves, and a retry may replace it; the last attempt
// closes the child with the marker, "not attempted", which is true of it.
//
// A second leg: its session's first leg ran and may have acted, so "not
// attempted" is never written for it. While the queue holds another attempt
// nothing is delivered and the session stays active; the last attempt
// delivers the one sentence the operator approved, as partial, and the cost
// row carries the sums of the leg before it.
func TestTheLastFailureBeforeStartClosesTheChild(t *testing.T) {
	const goal = "count the beans"
	for _, c := range []struct {
		name                  string
		leg, calls, tokens    int
		between, last, class  string // the session's result while an attempt is left; after the last; its evidence
		betweenStatus, notice string
	}{
		{name: "a first leg is not attempted", leg: 1,
			between: "unserved: failed before start: blew up", betweenStatus: "delivered",
			last: "unserved: failed before start: blew up", class: store.EvidenceNotRun,
			notice: "[sub-agent done] count the beans — unserved (failed)"},
		{name: "a later leg is partial, and says the legs before it ran", leg: 2, calls: 7, tokens: 900,
			between: "", betweenStatus: "active",
			last:   "partial: UNFINISHED — leg 2 of 4 could not start (blew up), and nothing continues it. The legs before it ran, and their calls may already have acted.",
			class:  store.EvidencePartialOrMixed,
			notice: "[sub-agent done] count the beans — partial"},
	} {
		t.Run(c.name, func(t *testing.T) {
			st, err := store.NewForTest(filepath.Join(t.TempDir(), "aii.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			a := New(&Config{SourcePath: filepath.Join(t.TempDir(), "config.json")})
			a.store = st
			defer func() { a.bgCancel(); a.bgWG.Wait() }() // the harvest wake is the app's goroutine
			h := &subagentHandler{a}
			const id = "ws_spent"
			queueChild(t, st, id)
			if err := st.StartWorkSession(t.Context(), id, store.SubagentDescription(goal)); err != nil {
				t.Fatal(err)
			}
			p := identity.SubagentRequest{Goal: goal, Depth: 1, SessionID: id, WallSeconds: 5, Context: "folded", Leg: c.leg, AccCalls: c.calls, AccTokens: c.tokens, AccWallMs: 1200}
			notices := func() (n int) {
				t.Helper()
				if err := st.DB().QueryRow(`SELECT COUNT(*) FROM outbox WHERE id = ?`, "subagent_"+id).Scan(&n); err != nil {
					t.Fatal(err)
				}
				return n
			}
			fleetSpent := func() bool {
				a.turn.mu.Lock()
				defer a.turn.mu.Unlock()
				return a.turn.askFleetSpent
			}

			if err := h.failBeforeStart(t.Context(), &store.WorkItem{RetryCount: 1, MaxRetries: 3}, p, c.leg, 4, "compose", errors.New("blew up")); err == nil {
				t.Fatal("a failure before start is returned, so the queue retries the item")
			}
			if ws, err := st.WorkSessionByID(id); err != nil || ws == nil || ws.Status != c.betweenStatus || ws.Result != c.between {
				t.Fatalf("with an attempt left the session is %+v (%v), want status %q and result %q", ws, err, c.betweenStatus, c.between)
			}
			if notices() != 0 || fleetSpent() {
				t.Fatalf("the child was closed while the queue still held an attempt: notices=%d fleet spent=%v", notices(), fleetSpent())
			}

			err = h.failBeforeStart(t.Context(), &store.WorkItem{RetryCount: 2, MaxRetries: 3}, p, c.leg, 4, "compose", errors.New("blew up"))
			if err == nil || err.Error() != "subagent compose: blew up" {
				t.Fatalf("the last failure is still returned, for the queue's own record: %v", err)
			}
			if ws, err := st.WorkSessionByID(id); err != nil || ws == nil || ws.Status != "delivered" || ws.Result != c.last || ws.Evidence != c.class {
				t.Fatalf("the child is closed with %+v (%v), want result %q as %s", ws, err, c.last, c.class)
			}
			var notice string
			if err := st.DB().QueryRow(`SELECT content FROM outbox WHERE id = ?`, "subagent_"+id).Scan(&notice); err != nil || notice != c.notice {
				t.Fatalf("the operator is told the child ended, in one line: %q %v, want %q", notice, err, c.notice)
			}
			var failed, calls, tokens, legs int
			if err := st.DB().QueryRow(`SELECT failed, calls, tokens, legs FROM subagent_metrics WHERE session_id = ?`, id).Scan(&failed, &calls, &tokens, &legs); err != nil || failed != 1 || calls != c.calls || tokens != c.tokens || legs != c.leg {
				t.Fatalf("the cost row is written as failed, with the earlier legs' sums: failed=%d calls=%d tokens=%d legs=%d %v", failed, calls, tokens, legs, err)
			}
			if !fleetSpent() {
				t.Fatal("a child that ended failed leaves the fleet spent, so a yield for its delivery must answer")
			}
		})
	}
}

// unreadableIdentity is an identity projection that cannot be read, so a
// prompt cannot be composed: a failure before a leg's start.
type unreadableIdentity struct{}

func (unreadableIdentity) PromptIdentity() (storerows.PromptIdentity, error) {
	return storerows.PromptIdentity{}, errors.New("the projection is unreadable")
}

// THE SAME, THROUGH THE QUEUE. A second leg whose prompt cannot be composed
// is retried by the queue, and its session stays active while it is: a
// delivery on an attempt in between would leave the next attempt a
// delivered session, which it would not run and could not close. The
// attempt that spends the retries closes the child with the sentence, and
// its M is the bound the operator configured, here 3, never the leg's own
// number. The model is never called.
func TestALaterLegThatCannotStartIsClosedThroughTheQueue(t *testing.T) {
	srv, n := deliveringChild(t)
	a, st, _ := legFixture(t, srv.URL, AgencyConfig{MaxToolRounds: 6, SubagentMaxToolRounds: 6, SubagentMaxToolCalls: 64, SubagentMaxLegs: 3, SubagentWallSeconds: 5})
	a.composer.SetIdentitySource(unreadableIdentity{})
	const id, goal = "ws_nocompose", "count the beans with no prompt"
	if err := st.StartWorkSession(t.Context(), id, store.SubagentDescription(goal)); err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(identity.SubagentRequest{Goal: goal, Depth: 1, SessionID: id, WallSeconds: 5, Context: "folded", Leg: 2, AccCalls: 7, AccTokens: 900})
	if _, err := st.EnqueueWork(&store.WorkItem{Kind: identity.SubagentWorkKind, Payload: string(payload), DedupKey: id + "#2", Source: "identity", LeaseMs: 65000}); err != nil {
		t.Fatal(err)
	}
	// A retry is claimed at the queue's next poke or its minute's
	// heartbeat; the test pokes, so the three attempts take their
	// backoffs and no more.
	poked := make(chan struct{})
	defer close(poked)
	go func() {
		for {
			select {
			case <-poked:
				return
			case <-time.After(100 * time.Millisecond):
				a.queueWake()
			}
		}
	}()
	ws := waitDelivered(t, st, id)
	const want = "partial: UNFINISHED — leg 2 of 3 could not start (compose identity projection: the projection is unreadable), and nothing continues it. The legs before it ran, and their calls may already have acted."
	if ws.Result != want || ws.Evidence != store.EvidencePartialOrMixed {
		t.Fatalf("the child is closed with %q as %s, want %q as %s", ws.Result, ws.Evidence, want, store.EvidencePartialOrMixed)
	}
	// The queue records the attempt after the handler returns, so the row
	// is read once it has settled.
	var state string
	var retries int
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		if err := st.DB().QueryRow(`SELECT state, retry_count FROM work_queue WHERE dedup_key = ?`, id+"#2").Scan(&state, &retries); err != nil {
			t.Fatal(err)
		}
		if state != "CLAIMED" || time.Now().After(deadline) {
			break
		}
	}
	if state != "FAILED" || retries != 3 {
		t.Fatalf("the leg's row is %s after %d attempts, want FAILED after 3: the child is closed on the attempt that spends them", state, retries)
	}
	if n.Load() != 0 {
		t.Fatalf("a leg that could not start called the model %d time(s)", n.Load())
	}
}
