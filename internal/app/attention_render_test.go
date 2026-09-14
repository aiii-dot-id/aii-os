package app

import (
	"strings"
	"testing"
)

// .
// .
// .
// .
func TestAttentionRendersOneMediumItemAndIsSuppressed(t *testing.T) {
	a, _ := focusFixture(t)
	state, err := a.buildWorkState()
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
		exec(`INSERT INTO ledger (seq, prev, ts, type, ring, payload, content, sig) VALUES (?, '', '2026-09-01T00:00:00Z', 'test', 3, '{}', '', '')`, seq)
	}
	exec(`INSERT INTO beliefs (id, statement, ring, confidence, evidence_count, first_seq, last_seq) VALUES ('b_a', 'the lighthouse is red', 3, 0.8, 1, 900001, 900001)`)
	exec(`INSERT INTO beliefs (id, statement, ring, confidence, evidence_count, first_seq, last_seq) VALUES ('b_b', 'the lighthouse is white', 3, 0.8, 1, 900002, 900002)`)
	exec(`INSERT INTO edges (id, from_id, to_id, edge_type, created_seq) VALUES ('ed_t', 'b_a', 'b_b', 'CONTRADICTS', 900003)`)
	state, err = a.buildWorkState()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(state, "### Attention") || !strings.Contains(state, "lighthouse is red") || !strings.Contains(state, "contradicts") {
		t.Fatalf("an open tension must render as the one attention item:\n%s", state)
	}
	if strings.Contains(state, "await the unconscious") {
		t.Fatalf("a silent item must never reach the turn:\n%s", state)
	}
	// .
	if err := a.store.StartWorkSession("ws_1", "some work"); err != nil {
		t.Fatal(err)
	}
	focus := "the active concern"
	dn := "confirm the endpoint before acting"
	if err := a.store.UpdateWorkPlan("ws_1", &focus, nil, nil, nil, nil, &dn); err != nil {
		t.Fatal(err)
	}
	state, _ = a.buildWorkState()
	if strings.Contains(state, "### Attention") {
		t.Fatalf("an owed decision must suppress the attention item:\n%s", state)
	}
}
