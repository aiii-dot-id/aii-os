package memory

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/store"
)

// .
// .
// .
// .
func TestAttentionIsComposedFromTheSources(t *testing.T) {
	s := newStore(t)
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	seedLedger(t, s, 8, now.Add(-400*24*time.Hour))
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := s.DB().Exec(q, args...); err != nil {
			t.Fatalf("%v\n%s", err, q)
		}
	}
	// .
	addBelief(t, s, "b_old", "the old lighthouse is red", 3, 1)
	exec(`INSERT INTO ledger (seq, prev, ts, type, ring, payload, content, sig) VALUES (99, '', ?, 'test.event', 3, '{}', '', '')`, now.Add(-time.Hour).UTC().Format(time.RFC3339Nano))
	exec(`INSERT INTO beliefs (id, statement, ring, confidence, evidence_count, first_seq, last_seq) VALUES ('b_new', 'the new lighthouse is white', 3, 0.8, 0, 99, 99)`)
	// .
	// .
	// .
	exec(`INSERT INTO ledger (seq, prev, ts, type, ring, payload, content, sig) VALUES (98, '', ?, 'test.event', 3, '{}', '', '')`, now.Add(-120*24*time.Hour).UTC().Format(time.RFC3339Nano))
	exec(`INSERT INTO beliefs (id, statement, ring, confidence, evidence_count, first_seq, last_seq) VALUES ('b_used', 'the tide table is on the wall', 3, 0.8, 1, 98, 98)`)
	exec(`INSERT INTO beliefs (id, statement, ring, confidence, evidence_count, first_seq, last_seq) VALUES ('b_unused', 'the tide table is by the door', 3, 0.8, 1, 98, 98)`)
	for i := 0; i < 10; i++ {
		if err := s.RecordMemoryAccess(now.Add(-time.Duration(i)*24*time.Hour), store.MemoryRef{Store: "beliefs", ID: "b_used"}); err != nil {
			t.Fatal(err)
		}
	}
	// .
	exec(`INSERT INTO edges (id, from_id, to_id, edge_type, created_seq) VALUES ('ed_t', 'b_old', 'b_new', 'CONTRADICTS', 3)`)
	exec(`INSERT INTO edges (id, from_id, to_id, edge_type, created_seq, archived) VALUES ('ed_gone', 'b_new', 'b_used', 'CONTRADICTS', 4, 1)`)
	// .
	exec(`INSERT INTO intentions (id, statement, state, created_seq, updated_seq) VALUES ('i_old', 'learn the tides', 'active', 5, 5)`)
	exec(`INSERT INTO intentions (id, statement, state, created_seq, updated_seq) VALUES ('i_new', 'paint the door', 'active', 6, 99)`)
	// .
	for i, id := range []string{"e1", "e2", "e3"} {
		addExperience(t, s, id, "an observation "+id, 6+i%2, now.Add(-time.Hour), false)
	}

	f := New(s)
	items, err := f.Attention(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string][]AttentionItem{}
	for _, it := range items {
		kinds[it.Kind] = append(kinds[it.Kind], it)
	}
	if got := kinds[AttentionDecayAlert]; len(got) != 2 || got[0].ID != "b_old" || got[1].ID != "b_unused" || got[0].Cost != CostLow || !strings.Contains(got[0].Text, "never recalled") {
		t.Fatalf("decay alerts = %+v, want b_old (a year unrecalled) then b_unused (four months, standard class); the fresh belief and the recalled one are not fading", got)
	}
	if got := kinds[AttentionContradiction]; len(got) != 1 || got[0].ID != "ed_t" || got[0].Cost != CostMedium || !strings.Contains(got[0].Text, "old lighthouse") {
		t.Fatalf("contradictions = %+v, want the live edge alone", got)
	}
	if got := kinds[AttentionFollowup]; len(got) != 1 || got[0].ID != "i_old" || got[0].Cost != CostLow {
		t.Fatalf("follow-ups = %+v, want the untouched intention alone", got)
	}
	if got := kinds[AttentionConsolidation]; len(got) != 1 || got[0].Cost != CostSilent {
		t.Fatalf("consolidation = %+v, want one silent item", got)
	}
	// .
	if items[0].Cost != CostMedium || items[len(items)-1].Cost != CostSilent {
		t.Fatalf("order: %+v", items)
	}
	// .
	if brief := AtMost(items, CostLow); len(brief) != 4 || brief[len(brief)-1].Cost != CostSilent {
		t.Fatalf("at most low = %+v", brief)
	}
	if turn := OfCost(items, CostMedium); len(turn) != 1 || turn[0].Kind != AttentionContradiction {
		t.Fatalf("medium = %+v", turn)
	}
	if text := RenderAttention(AtMost(items, CostLow)); strings.Contains(text, "backlog") || !strings.HasPrefix(text, "- ") {
		t.Fatalf("rendering: %s", text)
	}
}
