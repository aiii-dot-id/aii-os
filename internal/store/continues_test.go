package store

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/interaction"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
)

// STUCK IS READ FROM THE RECORD (the thresholds
// OpenHands documents): the same call with the same result 4 times, the
// same call failing more than 3 times (a nudge at 3), 3 replies in a row
// with no call, two calls alternating 6 times, over the last 20 events.
func TestStuckIsReadFromTheRecord(t *testing.T) {
	call := func(c, r string, failed bool) stuckEvent { return stuckEvent{call: c, result: r, failed: failed} }
	reply := stuckEvent{isReply: true}
	outcome := stuckEvent{outcome: true}
	alternate := func(times int, changing bool) []stuckEvent {
		var ev []stuckEvent
		for i := 0; i < times; i++ {
			ra, rb := "1", "2"
			if changing {
				ra, rb = fmt.Sprint(2*i), fmt.Sprint(2*i+1)
			}
			ev = append(ev, call("a", ra, false), call("b", rb, false))
		}
		return ev
	}
	th := DefaultStuckThresholds
	cases := []struct {
		name   string
		events []stuckEvent
		stuck  bool
		nudge  bool
	}{
		{"four of the same call and result", []stuckEvent{call("ls a", "x", false), call("ls a", "x", false), call("ls a", "x", false), call("ls a", "x", false)}, true, false},
		{"three of the same call and result", []stuckEvent{call("ls a", "x", false), call("ls a", "x", false), call("ls a", "x", false)}, false, false},
		{"the same call with different results", []stuckEvent{call("ls a", "1", false), call("ls a", "2", false), call("ls a", "3", false), call("ls a", "4", false), call("ls a", "5", false)}, false, false},
		{"three failures nudge", []stuckEvent{call("sh x", "e1", true), call("sh x", "e2", true), call("sh x", "e3", true)}, false, true},
		{"four failures stop", []stuckEvent{call("sh x", "e1", true), call("sh x", "e2", true), call("sh x", "e3", true), call("sh x", "e4", true)}, true, false},
		{"three replies in a row", []stuckEvent{call("ls a", "x", false), reply, reply, reply}, true, false},
		{"replies with calls between", []stuckEvent{reply, call("ls a", "x", false), reply, call("ls b", "y", false), reply}, false, false},
		{"alternating six times with the same results", alternate(6, false), true, false},
		{"alternating five times with the same results", alternate(5, false), true, false}, // the same-result rule reads the pair first
		{"alternating six times with changing results", alternate(6, true), false, false},
		// A recorded outcome wipes the slate (a review, 2026-11-06, finding 4).
		{"four of the same, an outcome, then one more", []stuckEvent{call("ls a", "x", false), call("ls a", "x", false), call("ls a", "x", false), call("ls a", "x", false), outcome, call("ls a", "x", false)}, false, false},
		{"two of the same, an outcome, two more", []stuckEvent{call("ls a", "x", false), call("ls a", "x", false), outcome, call("ls a", "x", false), call("ls a", "x", false)}, false, false},
		{"three failures, an outcome, then a failure", []stuckEvent{call("sh x", "e", true), call("sh x", "e", true), call("sh x", "e", true), outcome, call("sh x", "e", true)}, false, false},
		// A monologue is the tail, not three replies anywhere in the window.
		{"three replies then a call", []stuckEvent{reply, reply, reply, call("ls a", "x", false)}, false, false},
		{"work interleaved with replies", []stuckEvent{call("a", "1", false), reply, call("b", "2", false), reply, call("c", "3", false), reply, call("d", "4", false)}, false, false},
	}
	for _, c := range cases {
		stuck, nudge, err := readStuck(c.events, th)
		if err != nil || (stuck != "") != c.stuck || (nudge != "") != c.nudge {
			t.Errorf("%s: stuck %q nudge %q err %v, want stuck %v nudge %v", c.name, stuck, nudge, err, c.stuck, c.nudge)
		}
	}
	// On the record: the window follows the operator's last word.
	s := testStore(t)
	ctx := interaction.WithTurnSource(interaction.WithTurn(context.Background(), "turn-op"), "operator")
	if err := s.StartWorkSession(ctx, "ws_s", "work"); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 4; i++ {
		turn := "turn-" + string(rune('a'+i))
		if err := s.RecordToolStart(turn, 1, "main", "m", "", "ls", `{"path":"a"}`); err != nil {
			t.Fatal(err)
		}
		if err := s.RecordToolDoneMeasured(turn, 1, "ls", `{"path":"a"}`, "x", interaction.Succeeded, false, ToolMeasurement{}); err != nil {
			t.Fatal(err)
		}
	}
	if stuck, _, err := s.SessionStuck("ws_s", th); err != nil || stuck == "" {
		t.Fatalf("four identical calls on the record did not read as stuck: %q %v", stuck, err)
	}
	// A word in another session's turn is that session's (finding 3).
	other := interaction.WithResumeSession(interaction.WithTurnSource(interaction.WithTurn(context.Background(), "turn-other"), "operator"), "ws_other")
	if _, err := s.RecordConversation(other, "operator", "about the other work", "turn-other", interaction.Details{}); err != nil {
		t.Fatal(err)
	}
	if stuck, _, err := s.SessionStuck("ws_s", th); err != nil || stuck == "" {
		t.Fatalf("a word in another session's turn opened this session's window: %q %v", stuck, err)
	}
	if err := s.AddConversationTurn("operator", "try another way"); err != nil {
		t.Fatal(err)
	}
	if stuck, _, err := s.SessionStuck("ws_s", th); err != nil || stuck != "" {
		t.Fatalf("the operator's word did not open a fresh window: %q %v", stuck, err)
	}
	// Replies count in the session's own turns only: three in turns of
	// no session's are not its monologue; three in its fresh turns are.
	for i := 1; i <= 3; i++ {
		if err := s.AddConversationTurn("resident", "thinking"); err != nil {
			t.Fatal(err)
		}
	}
	if stuck, _, err := s.SessionStuck("ws_s", th); err != nil || stuck != "" {
		t.Fatalf("replies in turns of no session's read as this session's monologue: %q %v", stuck, err)
	}
	for i := 1; i <= 3; i++ {
		leg := interaction.WithResumeSession(interaction.WithTurnSource(interaction.WithTurn(context.Background(), fmt.Sprintf("turn-leg-%d", i)), "continuation"), "ws_s")
		turn := fmt.Sprintf("turn-leg-%d", i)
		if _, err := s.RecordConversation(leg, "system", "[work continues]", turn, interaction.Details{}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.RecordConversation(leg, "resident", "thinking", turn, interaction.Details{}); err != nil {
			t.Fatal(err)
		}
	}
	if stuck, _, err := s.SessionStuck("ws_s", th); err != nil || !strings.Contains(stuck, "3 replies in a row") {
		t.Fatalf("three replies in the session's own fresh turns did not read as its monologue: %q %v", stuck, err)
	}
	// A recorded outcome after them wipes the slate.
	if err := s.UpdateWorkState(ctx, "ws_s", "the figures are in"); err != nil {
		t.Fatal(err)
	}
	if stuck, _, err := s.SessionStuck("ws_s", th); err != nil || stuck != "" {
		t.Fatalf("a recorded outcome did not wipe the slate: %q %v", stuck, err)
	}
	// THE SLATE IS WIPED ONLY BY A CHANGED OUTCOME (a review, 2026-11-06):
	// four identical failures, then the same state written again, then a
	// delivery that never ran: the pattern stands; a changed state clears it.
	for i := 1; i <= 4; i++ {
		turn := fmt.Sprintf("turn-fail-%d", i)
		if err := s.RecordToolStart(turn, 1, "main", "m", "", "sh", `{"cmd":"x"}`); err != nil {
			t.Fatal(err)
		}
		if err := s.RecordToolDoneMeasured(turn, 1, "sh", `{"cmd":"x"}`, "Error: no", interaction.Failed, false, ToolMeasurement{}); err != nil {
			t.Fatal(err)
		}
	}
	if stuck, _, err := s.SessionStuck("ws_s", th); err != nil || !strings.Contains(stuck, "failed 4 times") {
		t.Fatalf("four identical failures did not read as stuck: %q %v", stuck, err)
	}
	if err := s.UpdateWorkState(ctx, "ws_s", "the figures are in"); err != nil {
		t.Fatal(err)
	}
	if stuck, _, err := s.SessionStuck("ws_s", th); err != nil || stuck == "" {
		t.Fatalf("an unchanged state rewrite wiped the slate: %q %v", stuck, err)
	}
	if err := s.StartWorkSession(ctx, "ws_s_child", "a child"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeliverWorkSession(ctx, "ws_s_child", "nothing", EvidenceNotRun, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.MeetWorkCondition(ctx, "ws_s", "delivery:ws_s_child", "A delivery came"); err != nil {
		t.Fatal(err)
	}
	if stuck, _, err := s.SessionStuck("ws_s", th); err != nil || stuck == "" {
		t.Fatalf("a delivery that never ran wiped the slate: %q %v", stuck, err)
	}
	if err := s.UpdateWorkState(ctx, "ws_s", "all the figures are in"); err != nil {
		t.Fatal(err)
	}
	if stuck, _, err := s.SessionStuck("ws_s", th); err != nil || stuck != "" {
		t.Fatalf("a changed outcome did not wipe the slate: %q %v", stuck, err)
	}
}

// PROGRESS IS A RECORDED OUTCOME: state, result,
// evidence, evidence read back, or a delivery met; a plan rewritten, a
// declaration, or the host's own condition notices are not.
func TestProgressIsARecordedOutcome(t *testing.T) {
	s := testStore(t)
	ctx := interaction.WithTurnSource(interaction.WithTurn(context.Background(), "turn-1"), "operator")
	if err := s.StartWorkSession(ctx, "ws_p", "work"); err != nil {
		t.Fatal(err)
	}
	var since uint64
	_ = s.db.QueryRow(`SELECT MAX(turn_seq) FROM conversations`).Scan(&since)
	plan := "a new plan"
	if err := s.UpdateWorkPlanStep(ctx, "ws_p", nil, nil, &plan, nil, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	now := "now"
	if err := s.SetWorkPromise(ctx, "ws_p", nil, &now, "the host's own"); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.SessionProgressSince("ws_p", since); ok || err != nil {
		t.Fatalf("a plan rewritten and a host notice counted as progress: %v %v", ok, err)
	}
	if err := s.UpdateWorkState(ctx, "ws_p", "half the figures are in"); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.SessionProgressSince("ws_p", since); !ok || err != nil {
		t.Fatalf("a recorded state did not count as progress: %v %v", ok, err)
	}
	// The same state written again is nothing new (a review, finding 7).
	_ = s.db.QueryRow(`SELECT MAX(turn_seq) FROM conversations`).Scan(&since)
	if err := s.UpdateWorkState(ctx, "ws_p", "half the figures are in"); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.SessionProgressSince("ws_p", since); ok || err != nil {
		t.Fatalf("a state rewritten unchanged counted as progress: %v %v", ok, err)
	}
	if err := s.UpdateWorkState(ctx, "ws_p", "all the figures are in"); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.SessionProgressSince("ws_p", since); !ok || err != nil {
		t.Fatalf("a changed state did not count as progress: %v %v", ok, err)
	}
	// A delivery is weighed by its evidence: a child that never ran, or
	// was refused before any effect, moved nothing; a worker's report of
	// an effect is an outcome recorded.
	_ = s.db.QueryRow(`SELECT MAX(turn_seq) FROM conversations`).Scan(&since)
	deliver := func(child, evidence string) {
		t.Helper()
		if err := s.StartWorkSession(ctx, child, "a child"); err != nil {
			t.Fatal(err)
		}
		if err := s.DeliverWorkSession(ctx, child, "nothing: "+child, evidence, ""); err != nil {
			t.Fatal(err)
		}
		if err := s.MeetWorkCondition(ctx, "ws_p", "delivery:"+child, "A delivery from "+child+" came"); err != nil {
			t.Fatal(err)
		}
	}
	deliver("ws_c1", EvidenceNotRun)
	if ok, err := s.SessionProgressSince("ws_p", since); ok || err != nil {
		t.Fatalf("a delivery that never ran counted as progress: %v %v", ok, err)
	}
	deliver("ws_c2", EvidenceRejectedNoEffect)
	if ok, err := s.SessionProgressSince("ws_p", since); ok || err != nil {
		t.Fatalf("a delivery refused before any effect counted as progress: %v %v", ok, err)
	}
	deliver("ws_c3", EvidenceWorkerReportOnly)
	if ok, err := s.SessionProgressSince("ws_p", since); !ok || err != nil {
		t.Fatalf("a worker's reported delivery did not count as progress: %v %v", ok, err)
	}
}

// A COMPLETION IS AUDITED BY ITS DELIVERY (a review, 2026-11-06, finding 8):
// a session that carried the promise, delivered with evidence of a
// verified class and read back; a worker's report is the identity's claim.
func TestACompletionIsAuditedByItsDelivery(t *testing.T) {
	s := testStore(t)
	ctx := interaction.WithTurnSource(interaction.WithTurn(context.Background(), "turn-1"), "operator")
	under := func(id, promise string) {
		t.Helper()
		if err := s.StartWorkSession(ctx, id, "keep "+promise); err != nil {
			t.Fatal(err)
		}
		p := promise
		if err := s.SetWorkPromise(ctx, id, &p, nil, ""); err != nil {
			t.Fatal(err)
		}
	}
	// A worker's report, even with a readback written, is the identity's
	// claim: readback text alone is not independent verification.
	under("ws_claim", "cm_a")
	if err := s.DeliverWorkSession(ctx, "ws_claim", "served: done", EvidenceWorkerReportOnly, "the worker says it is there"); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.CompletionAudited("cm_a"); ok || err != nil {
		t.Fatalf("a worker's report audited the completion: %v %v", ok, err)
	}
	under("ws_checked", "cm_a")
	if err := s.DeliverWorkSession(ctx, "ws_checked", "served: done", EvidenceLocallyVerified, "the file is there, 3 rows"); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.CompletionAudited("cm_a"); !ok || err != nil {
		t.Fatalf("a locally verified delivery, read back, did not audit the completion: %v %v", ok, err)
	}
	if ok, _ := s.CompletionAudited("cm_none"); ok {
		t.Fatal("a promise no session carried reads audited")
	}
	// The widening read marks each completion.
	operatorRelationship(t, s, 1, "rel_q", "Quinn", "founding_operator", "")
	seqEvent(t, s, 2, ledger.EventCommitmentPromised, map[string]any{"id": "cm_a", "counterpart_id": "rel_q", "description": "the report"})
	seqEvent(t, s, 3, ledger.EventCommitmentStateChange, map[string]any{"id": "cm_a", "state": "completed", "result": "sent"})
	seqEvent(t, s, 4, ledger.EventCommitmentPromised, map[string]any{"id": "cm_b", "counterpart_id": "rel_q", "description": "the figures"})
	seqEvent(t, s, 5, ledger.EventCommitmentStateChange, map[string]any{"id": "cm_b", "state": "completed", "result": "filed"})
	completed, _, err := s.WideningCounts("rel_q", 0)
	if err != nil || len(completed) != 2 || !completed[0].Audited || completed[1].Audited {
		t.Fatalf("the completions' audit marks: %+v %v, want cm_a audited and cm_b a claim", completed, err)
	}
}
