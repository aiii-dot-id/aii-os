package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/continuity/continuitytest"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// Attention in the turn (phase 3): one medium-cost item — an open
// tension — renders when nothing urgent is owed, after the mandatory
// recovery information; an owed operator decision suppresses it, like
// the curiosity cue; nothing renders when the record holds nothing.
func TestAttentionRendersOneMediumItemAndIsSuppressed(t *testing.T) {
	a, _ := focusFixture(t)
	state, err := a.buildTurnFacts(t.Context(), false)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(state, "### Attention") {
		t.Fatalf("nothing held, yet an attention block appeared:\n%s", state)
	}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := a.store.DB().Exec(q, args...); err != nil {
			t.Fatalf("%v\n%s", err, q)
		}
	}
	for _, seq := range []int{900001, 900002, 900003} {
		exec(`INSERT INTO ledger (seq, prev, ts, type, ring, payload, content) VALUES (?, '', '2026-09-01T00:00:00Z', 'test', 3, '{}', '')`, seq)
	}
	exec(`INSERT INTO beliefs (id, statement, ring, confidence, evidence_count, first_seq, last_seq) VALUES ('b_a', 'the lighthouse is red', 3, 0.8, 1, 900001, 900001)`)
	exec(`INSERT INTO beliefs (id, statement, ring, confidence, evidence_count, first_seq, last_seq) VALUES ('b_b', 'the lighthouse is white', 3, 0.8, 1, 900002, 900002)`)
	exec(`INSERT INTO edges (id, from_id, to_id, edge_type, created_seq) VALUES ('ed_t', 'b_a', 'b_b', 'CONTRADICTS', 900003)`)
	state, err = a.buildTurnFacts(t.Context(), false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(state, "### Attention") || !strings.Contains(state, "lighthouse is red") || !strings.Contains(state, "contradicts") {
		t.Fatalf("an open tension must render as the one attention item:\n%s", state)
	}
	if strings.Contains(state, "await the unconscious") {
		t.Fatalf("a silent item must never reach the turn:\n%s", state)
	}
	// An owed decision suppresses it.
	if err := a.store.StartWorkSession(t.Context(), "ws_1", "some work"); err != nil {
		t.Fatal(err)
	}
	focus := "the active concern"
	dn := "confirm the endpoint before acting"
	if err := a.store.UpdateWorkPlan(t.Context(), "ws_1", &focus, nil, nil, nil, nil, &dn); err != nil {
		t.Fatal(err)
	}
	state, _ = a.buildTurnFacts(t.Context(), false)
	if strings.Contains(state, "### Attention") {
		t.Fatalf("an owed decision must suppress the attention item:\n%s", state)
	}
}

func TestATurnsFactsAreReadUnderTheTurnsContext(t *testing.T) {
	a, _ := focusFixture(t)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := a.store.DB().Exec(q, args...); err != nil {
			t.Fatalf("%v\n%s", err, q)
		}
	}
	for _, seq := range []int{900001, 900002, 900003} {
		exec(`INSERT INTO ledger (seq, prev, ts, type, ring, payload, content) VALUES (?, '', '2026-09-01T00:00:00Z', 'test', 3, '{}', '')`, seq)
	}
	exec(`INSERT INTO beliefs (id, statement, ring, confidence, evidence_count, first_seq, last_seq) VALUES ('b_a', 'the lighthouse is red', 3, 0.8, 1, 900001, 900001)`)
	exec(`INSERT INTO beliefs (id, statement, ring, confidence, evidence_count, first_seq, last_seq) VALUES ('b_b', 'the lighthouse is white', 3, 0.8, 1, 900002, 900002)`)
	exec(`INSERT INTO edges (id, from_id, to_id, edge_type, created_seq) VALUES ('ed_t', 'b_a', 'b_b', 'CONTRADICTS', 900003)`)
	if _, err := a.store.RecordInbound("in_mail_ctx", "mail", "friend", "CTX_ARRIVAL_3f1e"); err != nil {
		t.Fatal(err)
	}
	// Control: a live turn reads both.
	facts, err := a.buildTurnFacts(t.Context(), true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(facts, "### Attention") || !strings.Contains(facts, "CTX_ARRIVAL_3f1e") {
		t.Fatalf("a live turn must carry the attention item and the pending arrival:\n%s", facts)
	}
	ended, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := a.buildTurnFacts(ended, true); !errors.Is(err, context.Canceled) {
		t.Fatalf("an ended turn's pending-events read = %v, want the turn's own cancellation", err)
	}
	facts, err = a.buildTurnFacts(ended, false)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(facts, "### Attention") {
		t.Fatalf("an ended turn still read the attention item:\n%s", facts)
	}
}

// THE TURN IS TOLD ONLY WHEN IT IS NOT WELL, in the one slot attention has,
// and told the act that answers it. A healthy pass says nothing to the turn.
func TestTheTurnHearsOfContinuityOnlyWhenItIsNotWell(t *testing.T) {
	a, _, keyPath := maintApp(t)
	appendFixtureEvent(t, a, keyPath, "exp_a")
	a.continuity.EnsureSnapshotKey(continuitySettingsOf(a.configSnapshot()))
	_, recipient := continuitytest.SnapshotKey(t, keyPath)
	continuitytest.CheckedEscrow(t, keyPath, a.door.kp, recipient)
	maintenancePass(t, a)
	state, err := a.buildTurnFacts(t.Context(), false)
	if err != nil {
		t.Fatal(err)
	}
	for _, word := range []string{"snapshot", "backup.", "continuity"} {
		if strings.Contains(state, word) {
			t.Fatalf("A HEALTHY PASS REACHED THE TURN (%q):\n%s", word, state)
		}
	}
	if err := a.store.SetContinuityStatus(store.ContinuityStatus{Outcome: store.ContinuityFailed, Detail: "copy: no space left on device"}); err != nil {
		t.Fatal(err)
	}
	state, err = a.buildTurnFacts(t.Context(), false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(state, "### Attention") || !strings.Contains(state, "work action=backup.take") {
		t.Errorf("a failing pass did not reach the turn's attention slot:\n%s", state)
	}
	if strings.Contains(state, "no space left") {
		t.Errorf("what failed is the operator's, and the turn was shown it:\n%s", state)
	}
}
