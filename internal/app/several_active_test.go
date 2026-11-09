package app

import (
	"bytes"
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/conversation"
	"github.com/aiii-dot-id/aii-os/internal/identity"
	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/store"
	"github.com/aiii-dot-id/aii-os/internal/workrecord"
)

// several_active_test.go — a call made while more than one main work
// session is active is counted to the newest and says so (the
// learning-readiness critique's C3, 2026-10-27; no new column, R243): the
// call row resolves the seat once (RecordToolStart) and the call is run,
// completed and read from it (F1, 2026-11-01), so the reason survives the
// call's completion into every reader of the record. Falsifier: a call row names a session without several_active
// while two main sessions were active at its start.

// seatCall runs one tool call as the loop does: its start recorded, then
// between, then the call from its start's seat, then its completion, and
// answers the observation.
func seatCall(t *testing.T, a *App, ctx context.Context, actor, turn, dir string, between func()) conversation.Observation {
	t.Helper()
	args := `{"path":` + strconv.Quote(dir) + `}`
	var tc llm.ToolCall
	tc.ID, tc.Function.Name, tc.Function.Arguments = "c_"+turn, "ls", args
	obs := startedExecute(t, a, ctx, actor, turn, tc, between)
	tr := appTranscript{st: a.store, safe: a.safeTools, actor: actor}
	if err := tr.RecordToolDone(turn, 1, "ls", args, obs); err != nil {
		t.Fatal(err)
	}
	return obs
}

// rowSeat is the session and reason a call's row of kind holds.
func rowSeat(t *testing.T, a *App, turn, kind string) (string, string) {
	t.Helper()
	var session, reason string
	if err := a.store.DB().QueryRow(`SELECT COALESCE(json_extract(details,'$.work_session_id'),''), COALESCE(json_extract(details,'$.session_reason'),'')
		FROM conversations WHERE turn_id=? AND kind=?`, turn, kind).Scan(&session, &reason); err != nil {
		t.Fatalf("%s row of %s: %v", kind, turn, err)
	}
	return session, reason
}

func TestTheRecordAndTheSeatAgreeWhenSeveralAreActive(t *testing.T) {
	a, dir := dispatchApp(t)
	t.Cleanup(func() { stopApp(t, a) })
	ctx := t.Context()
	agree := func(turn, wantSession, wantReason string, obs conversation.Observation) {
		t.Helper()
		if obs.WorkSession != wantSession || obs.SessionReason != wantReason {
			t.Errorf("%s: the seat says %q %q, want %q %q", turn, obs.WorkSession, obs.SessionReason, wantSession, wantReason)
		}
		for _, kind := range []string{"tool_call", "tool_result"} {
			if s, r := rowSeat(t, a, turn, kind); s != wantSession || r != wantReason {
				t.Errorf("%s: its %s row says %q %q, want %q %q", turn, kind, s, r, wantSession, wantReason)
			}
		}
	}

	// One main session: counted to it, no reason.
	if err := a.store.StartWorkSession(ctx, "ws_older", "the older work"); err != nil {
		t.Fatal(err)
	}
	agree("t_one", "ws_older", "", seatCall(t, a, ctx, "", "t_one", dir, nil))

	// Two: counted to the newest, and marked, by the seat and the record.
	if err := a.store.StartWorkSession(ctx, "ws_newer", "the newer work"); err != nil {
		t.Fatal(err)
	}
	agree("t_two", "ws_newer", store.SeveralActive, seatCall(t, a, ctx, "", "t_two", dir, nil))

	// A child's call is its own session's, never marked.
	if _, err := a.store.DB().Exec(`INSERT INTO work_queue (id, kind, payload, dedup_key, source, state) VALUES ('wq_child', ?, '{}', 'ws_child', 'identity', 'DONE')`, identity.SubagentWorkKind); err != nil {
		t.Fatal(err)
	}
	if err := a.store.StartWorkSession(ctx, "ws_child", store.SubagentDescription("a bounded child task")); err != nil {
		t.Fatal(err)
	}
	child := context.WithValue(ctx, identity.SubagentWorkSession{}, "ws_child")
	agree("t_child", "ws_child", "", seatCall(t, a, child, "ws_child", "t_child", dir, nil))

	// A main session delivered leaves one: the mark goes with the choice.
	if err := a.store.DeliverWorkSession(ctx, "ws_newer", "done", "", ""); err != nil {
		t.Fatal(err)
	}
	agree("t_after", "ws_older", "", seatCall(t, a, ctx, "", "t_after", dir, nil))

	// Work started after the call's start and before it runs moves nothing:
	// the call runs, is recorded and is read from its start's seat (F1).
	agree("t_focus", "ws_older", "", seatCall(t, a, ctx, "", "t_focus", dir, func() {
		if err := a.store.StartWorkSession(ctx, "ws_during", "work started mid-call"); err != nil {
			t.Fatal(err)
		}
	}))
	// And the next call sees both.
	agree("t_next", "ws_during", store.SeveralActive, seatCall(t, a, ctx, "", "t_next", dir, nil))

	// An operator's call is in no session, and says why in one spelling.
	if err := a.store.RecordToolStart("t_operator", 1, "operator", "", "", "ls", "{}"); err != nil {
		t.Fatal(err)
	}
	if s, r := rowSeat(t, a, "t_operator", "tool_call"); s != "" || r != "operator_act" {
		t.Errorf("an operator's call row says %q %q, want no session and operator_act", s, r)
	}

	// A lookup that fails is said to have failed, not taken for none.
	conn, err := a.store.DB().Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `ALTER TABLE work_sessions RENAME COLUMN status TO unreadable`); err != nil {
		t.Fatal(err)
	}
	seat := a.callSeat(ctx)
	if _, err := conn.ExecContext(context.Background(), `ALTER TABLE work_sessions RENAME COLUMN unreadable TO status`); err != nil {
		t.Fatal(err)
	}
	if seat.session != "" || seat.reason != "session_lookup_failed" || seat.lookup == nil {
		t.Errorf("a failed lookup reads as %+v", seat)
	}
}

// The reason a call's start recorded is the call's through its completion,
// the identity's read of its work and the operator's export, also when the
// executor names the session and no reason.
func TestSeveralActiveSurvivesCompletionRecallAndExport(t *testing.T) {
	a, dir := dispatchApp(t)
	t.Cleanup(func() { stopApp(t, a) })
	ctx := t.Context()
	for _, id := range []string{"ws_first", "ws_second"} {
		if err := a.store.StartWorkSession(ctx, id, "work "+id); err != nil {
			t.Fatal(err)
		}
	}
	seatCall(t, a, ctx, "", "t_seat", dir, nil)
	// A completion carries no session of its own: the start row's seat is
	// written whatever the observation says (store.appendToolOutcome, F1).
	tr := appTranscript{st: a.store, safe: a.safeTools}
	if err := tr.RecordToolStart("t_bare", 1, "c_bare", "ls", "{}", "m"); err != nil {
		t.Fatal(err)
	}
	if err := tr.RecordToolDone("t_bare", 1, "ls", "{}", conversation.Observation{Text: "listed", WorkSession: "ws_second"}); err != nil {
		t.Fatal(err)
	}
	for _, turn := range []string{"t_seat", "t_bare"} {
		if s, r := rowSeat(t, a, turn, "tool_result"); s != "ws_second" || r != store.SeveralActive {
			t.Errorf("%s: the completed record says %q %q", turn, s, r)
		}
	}

	// The identity's read of the session's calls.
	out, err := a.engine.ExecuteAction(ctx, "verb", "recall", map[string]any{"source": "work", "id": "ws_second", "query": ""})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(out, "begun while several sessions were active, counted to the newest") != 2 {
		t.Errorf("recall source=work does not say so of both calls:\n%s", out)
	}

	// The operator's export.
	var b bytes.Buffer
	if _, err := workrecord.Write(ctx, &b, a.store, nil, workrecord.Filter{Session: "ws_second"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	marked := 0
	for _, line := range strings.Split(strings.TrimSpace(b.String()), "\n") {
		var l struct {
			Calls []struct {
				SessionReason string `json:"session_reason"`
			} `json:"calls"`
		}
		if json.Unmarshal([]byte(line), &l) == nil {
			for _, c := range l.Calls {
				if c.SessionReason == store.SeveralActive {
					marked++
				}
			}
		}
	}
	if marked != 2 {
		t.Errorf("the export marks %d of the 2 calls several_active:\n%s", marked, b.String())
	}

	// work measure counts them.
	measured, err := a.engine.ExecuteAction(ctx, "verb", "work", map[string]any{"action": "measure"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(measured, "calls_while_several_sessions_active: 2") {
		t.Errorf("work measure does not count the calls:\n%s", measured)
	}
}

// A start while a main session is active says that session stays active
// and that calls are counted to the new one.
func TestAWorkStartSaysASessionIsStillActive(t *testing.T) {
	a, _ := dispatchApp(t)
	t.Cleanup(func() { stopApp(t, a) })
	ctx := t.Context()
	first, err := a.engine.ExecuteAction(ctx, "verb", "work", map[string]any{"action": "start", "description": "survey the stair"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(first, "still active") {
		t.Fatalf("a first start speaks of another session:\n%s", first)
	}
	older, err := a.store.ActiveWorkSession()
	if err != nil || older == nil {
		t.Fatal(older, err)
	}
	second, err := a.engine.ExecuteAction(ctx, "verb", "work", map[string]any{"action": "start", "description": "mend the rail"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(second, "Session "+older.ID+" (survey the stair) is still active; calls are counted to this new one until it is delivered.") {
		t.Fatalf("a second start does not say the first is still active:\n%s", second)
	}
}
