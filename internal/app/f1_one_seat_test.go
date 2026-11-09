package app

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/conversation"
	"github.com/aiii-dot-id/aii-os/internal/identity"
	"github.com/aiii-dot-id/aii-os/internal/interaction"
	"github.com/aiii-dot-id/aii-os/internal/limits"
	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
	"github.com/aiii-dot-id/aii-os/internal/store"
	"github.com/aiii-dot-id/aii-os/internal/tools"
)

// A TOOL CALL IS COUNTED TO THE SESSION ITS START RESOLVED, AND TO NOTHING
// ELSE (F1, 2026-11-01; the future-learning design's F1). The start row
// resolves the seat once, in its own transaction; the call's execution,
// the plugin's acting session, its completion and the record the identity
// and the operator read all take that one answer. Work started or
// delivered between the start and the execution moves none of them, and
// the next call reads its own start. Falsifier: any of them names the
// session active when the call ran rather than when it started. Control:
// the loop's executor resolving the seat again when the call runs.
func TestACallIsCountedToTheSessionItsStartResolved(t *testing.T) {
	a, _ := dispatchApp(t)
	t.Cleanup(func() { stopApp(t, a) })
	ctx := t.Context()
	var (
		mu    sync.Mutex
		heard []pluginhost.ActingSession
	)
	a.toolReg.Register(&fixtureTool{name: "pl_seat_probe", run: func(ctx context.Context) (tools.Result, error) {
		mu.Lock()
		heard = append(heard, pluginhost.ActingSessionFrom(ctx))
		mu.Unlock()
		return tools.Result{Output: "probed"}, nil
	}})
	probe := func(id string, ordinal int) llm.ToolCall {
		var tc llm.ToolCall
		tc.ID, tc.Function.Name, tc.Function.Arguments, tc.EmissionOrdinal = id, "pl_seat_probe", "{}", ordinal
		return tc
	}
	told := func() pluginhost.ActingSession {
		mu.Lock()
		defer mu.Unlock()
		if len(heard) == 0 {
			t.Fatal("the plugin was never called")
		}
		return heard[len(heard)-1]
	}
	tr := appTranscript{st: a.store, safe: a.safeTools}
	done := func(turn string, ordinal int, obs conversation.Observation) {
		t.Helper()
		if err := tr.RecordToolDone(turn, ordinal, "pl_seat_probe", "{}", obs); err != nil {
			t.Fatal(err)
		}
	}
	says := func(what, turn string, ordinal int, wantSession, wantReason string, obs conversation.Observation) {
		t.Helper()
		if obs.WorkSession != wantSession || obs.SessionReason != wantReason {
			t.Errorf("%s: the call ran in %q %q, want %q %q", what, obs.WorkSession, obs.SessionReason, wantSession, wantReason)
		}
		if p := told(); p.ID != wantSession || p.Reason != wantReason || p.CallID != store.ToolCallRecordID(turn, ordinal) {
			t.Errorf("%s: the plugin was told %+v, want %q %q for its own call", what, p, wantSession, wantReason)
		}
	}
	rows := func(what, turn, wantSession, wantReason string) {
		t.Helper()
		for _, kind := range []string{"tool_call", "tool_result"} {
			if s, r := rowSeat(t, a, turn, kind); s != wantSession || r != wantReason {
				t.Errorf("%s: its %s row says %q %q, want %q %q", what, kind, s, r, wantSession, wantReason)
			}
		}
	}

	if err := a.store.StartWorkSession(ctx, "ws_a", "the work begun first"); err != nil {
		t.Fatal(err)
	}
	// Started in ws_a; newer work starts before it runs.
	obs := startedExecute(t, a, ctx, "", "t_a", probe("c_a", 1), func() {
		if err := a.store.StartWorkSession(ctx, "ws_b", "work started while the call waited"); err != nil {
			t.Fatal(err)
		}
	})
	done("t_a", 1, obs)
	says("a call started before newer work", "t_a", 1, "ws_a", "", obs)
	rows("a call started before newer work", "t_a", "ws_a", "")
	parts, err := a.store.WorkRecordOf([]string{"ws_a", "ws_b"})
	if err != nil {
		t.Fatal(err)
	}
	if len(parts["ws_a"].Calls) != 1 || len(parts["ws_b"].Calls) != 0 {
		t.Errorf("the record both readers use counts it to ws_a alone: ws_a %d, ws_b %d", len(parts["ws_a"].Calls), len(parts["ws_b"].Calls))
	}

	// The next call reads its own start: the newest, marked.
	obs = startedExecute(t, a, ctx, "", "t_b", probe("c_b", 1), nil)
	done("t_b", 1, obs)
	says("the next call", "t_b", 1, "ws_b", store.SeveralActive, obs)
	rows("the next call", "t_b", "ws_b", store.SeveralActive)

	// Calls started together, as the loop starts parallel-safe calls before
	// any runs, keep their starts' answer when work is delivered between.
	for _, o := range []int{1, 2} {
		if err := tr.RecordToolStart("t_p", o, fmt.Sprintf("c_p%d", o), "pl_seat_probe", "{}", "m"); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.store.DeliverWorkSession(ctx, "ws_b", "done", "", ""); err != nil {
		t.Fatal(err)
	}
	for _, o := range []int{2, 1} {
		obs := appToolExecutor{a}.Execute(interaction.WithTurn(ctx, "t_p"), probe(fmt.Sprintf("c_p%d", o), o))
		done("t_p", o, obs)
		says(fmt.Sprintf("parallel call %d", o), "t_p", o, "ws_b", store.SeveralActive, obs)
	}

	// A call refused before it runs is recorded from its start too.
	if err := tr.RecordToolStart("t_refused", 1, "c_r", "pl_seat_probe", "{}", "m"); err != nil {
		t.Fatal(err)
	}
	if err := a.store.StartWorkSession(ctx, "ws_c", "work started after the refused call's start"); err != nil {
		t.Fatal(err)
	}
	done("t_refused", 1, conversation.Observation{Text: "refused", Failed: true, Refused: true, ReasonCode: "tool_not_offered"})
	rows("a refused call", "t_refused", "ws_a", "")

	// A start no record holds is a failed lookup, said as one.
	if seat := a.startedSeat(interaction.WithTurn(ctx, "t_unstarted"), probe("c_u", 1)); seat.session != "" || seat.reason != "session_lookup_failed" || seat.lookup == nil {
		t.Errorf("a call with no recorded start reads as %+v", seat)
	}
	// So is a start that cannot be read.
	conn, err := a.store.DB().Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `ALTER TABLE conversations RENAME COLUMN details TO unreadable`); err != nil {
		t.Fatal(err)
	}
	seat := a.startedSeat(interaction.WithTurn(ctx, "t_a"), probe("c_a", 1))
	if _, err := conn.ExecContext(context.Background(), `ALTER TABLE conversations RENAME COLUMN unreadable TO details`); err != nil {
		t.Fatal(err)
	}
	if seat.session != "" || seat.reason != "session_lookup_failed" || seat.lookup == nil {
		t.Errorf("an unreadable start reads as %+v", seat)
	}

	// SAFE diverts a start to its own record, which keeps the seat the
	// durable row would have resolved; the call reads it there.
	a.store.SetFrozen(true, "a test hold")
	if err := tr.RecordToolStart("t_safe", 1, "c_s", "pl_seat_probe", "{}", "m"); err != nil {
		t.Fatal(err)
	}
	seat = a.startedSeat(interaction.WithTurn(ctx, "t_safe"), probe("c_s", 1))
	a.store.SetFrozen(false, "")
	if seat.actor != actorSafe || seat.session != "ws_c" || seat.reason != store.SeveralActive || seat.lookup != nil {
		t.Errorf("a SAFE-diverted start reads as %+v, want SAFE's, ws_c several_active", seat)
	}
	if _, ok := a.safeTools.seat("t_safe", 1); !ok {
		t.Error("the SAFE record does not hold the diverted start")
	}
	// A start SAFE holds is read even when the durable record cannot be,
	// and after SAFE has ended: the call runs, and the plugin is told, the
	// seat its start resolved.
	a.store.SetFrozen(true, "a test hold")
	if err := tr.RecordToolStart("t_safe2", 1, "c_s2", "pl_seat_probe", "{}", "m"); err != nil {
		t.Fatal(err)
	}
	a.store.SetFrozen(false, "")
	if _, err := conn.ExecContext(ctx, `ALTER TABLE conversations RENAME COLUMN details TO unreadable`); err != nil {
		t.Fatal(err)
	}
	obs = appToolExecutor{a}.Execute(interaction.WithTurn(ctx, "t_safe2"), probe("c_s2", 1))
	if _, err := conn.ExecContext(context.Background(), `ALTER TABLE conversations RENAME COLUMN unreadable TO details`); err != nil {
		t.Fatal(err)
	}
	says("a SAFE-held start with the durable record unreadable", "t_safe2", 1, "ws_c", store.SeveralActive, obs)
	// Boot-SAFE's conversation writes its starts to its own SAFE record,
	// which resolves the seat itself from the read-only projection, or says
	// there is none where there is no projection.
	boot := &safeToolRecord{st: a.store}
	if err := boot.RecordToolStart("t_boot", 1, "c_boot", "pl_seat_probe", "{}", "m"); err != nil {
		t.Fatal(err)
	}
	if got, ok := boot.seat("t_boot", 1); !ok || got != (store.CallSeat{Actor: "safe", Session: "ws_c", Reason: store.SeveralActive}) {
		t.Errorf("boot-SAFE's start holds %+v (%v), want SAFE's, ws_c several_active", got, ok)
	}
	bare := &safeToolRecord{}
	if err := bare.RecordToolStart("t_bare", 1, "c_bare", "pl_seat_probe", "{}", "m"); err != nil {
		t.Fatal(err)
	}
	if got, _ := bare.seat("t_bare", 1); got != (store.CallSeat{Actor: "safe", Reason: "no_session"}) {
		t.Errorf("boot-SAFE with no projection holds %+v, want no session, said", got)
	}

	// A child's call is its own: counted to its session when the record
	// holds it, to none with the reason when it does not, and its
	// declaration feeds its leg's meter, keyed by the child as its start
	// recorded it.
	child := context.WithValue(ctx, identity.SubagentWorkSession{}, "ws_kid")
	kid := appTranscript{st: a.store, safe: a.safeTools, actor: "ws_kid"}
	if err := kid.RecordToolStart("t_kid0", 1, "c_k0", "work", "{}", "m"); err != nil {
		t.Fatal(err)
	}
	seat = a.startedSeat(interaction.WithTurn(child, "t_kid0"), probe("c_k0", 1))
	if seat.actor != actorSubagent || seat.leg != "ws_kid" || seat.session != "" || seat.reason != "acting_session_unavailable" {
		t.Errorf("a child whose session the record does not hold reads as %+v", seat)
	}
	if _, err := a.store.DB().Exec(`INSERT INTO work_queue (id, kind, payload, dedup_key, source, state) VALUES ('wq_kid', ?, '{}', 'ws_kid', 'identity', 'DONE')`, identity.SubagentWorkKind); err != nil {
		t.Fatal(err)
	}
	if err := a.store.StartWorkSession(ctx, "ws_kid", store.SubagentDescription("a bounded child task")); err != nil {
		t.Fatal(err)
	}
	meter, release := a.registerLegMeter("ws_kid")
	defer release()
	var update llm.ToolCall
	update.ID, update.Function.Name, update.Function.Arguments = "c_k1", "work", `{"action":"update","steps":7}`
	obs = startedExecute(t, a, child, "ws_kid", "t_kid1", update, nil)
	if obs.Failed || obs.WorkSession != "ws_kid" || obs.SessionReason != "" {
		t.Errorf("a child's own work call ran as %q %q: %s", obs.WorkSession, obs.SessionReason, obs.Text)
	}
	if remaining, _ := meter.get(); remaining != 7 {
		t.Errorf("the child's declaration reached its leg meter as %d, want 7", remaining)
	}
}

// THE LOOP'S OWN CALLS READ THEIR STARTS (F1). A child run by the real loop
// calls a tool and then delivers; the tool is told the child's session as
// the call's start resolved it, with the call's own record id. A call
// whose start the executor could not find would be told no session and a
// failed lookup, so this proves the loop records each start under the
// turn and ordinal its execution looks it up by.
func TestTheLoopsOwnCallsReadTheirStarts(t *testing.T) {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch n.Add(1) {
		case 1:
			_, _ = io.WriteString(w, `{"id":"c","model":"fake-child","choices":[{"index":0,"message":{"role":"assistant","content":"","tool_calls":[{"id":"call_1","type":"function","function":{"name":"pl_seat_probe","arguments":"{}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":100,"completion_tokens":10,"total_tokens":110}}`)
		default:
			_, _ = io.WriteString(w, `{"id":"c","model":"fake-child","choices":[{"index":0,"message":{"role":"assistant","content":"","tool_calls":[{"id":"call_2","type":"function","function":{"name":"work","arguments":"{\"action\":\"deliver\",\"result\":\"served: probed\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":100,"completion_tokens":10,"total_tokens":110}}`)
		}
	}))
	t.Cleanup(srv.Close)
	a, st, _ := legFixture(t, srv.URL, AgencyConfig{MaxToolRounds: limits.Of(6), SubagentMaxToolRounds: limits.Of(6), SubagentMaxToolCalls: limits.Of(64), SubagentMaxLegs: limits.Of(4), SubagentWallSeconds: 5})
	var (
		mu    sync.Mutex
		heard []pluginhost.ActingSession
	)
	a.toolReg.Register(&fixtureTool{name: "pl_seat_probe", run: func(ctx context.Context) (tools.Result, error) {
		mu.Lock()
		heard = append(heard, pluginhost.ActingSessionFrom(ctx))
		mu.Unlock()
		return tools.Result{Output: "probed"}, nil
	}})
	id := spawnChild(t, a, st, "probe and deliver")
	if ws := waitDelivered(t, st, id); ws.Result != "served: probed" {
		t.Fatalf("the child's delivery: %q", ws.Result)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(heard) != 1 {
		t.Fatalf("the probe was called %d times, want once", len(heard))
	}
	var turn string
	if err := st.DB().QueryRow(`SELECT turn_id FROM conversations WHERE kind='tool_call' AND json_extract(details,'$.tool')='pl_seat_probe'`).Scan(&turn); err != nil {
		t.Fatal(err)
	}
	if p := heard[0]; p.ID != id || p.Reason != "" || p.CallID != store.ToolCallRecordID(turn, 1) {
		t.Errorf("the loop's call was told %+v, want %q with no reason for its own call %s", p, id, store.ToolCallRecordID(turn, 1))
	}
}
