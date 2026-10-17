package memory

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/memory/attention"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// Attention is composed at read time from the sources that own it
// without a separate queue or visible counts: a fading belief, an open
// tension, an untouched intention, and
// the silent consolidation item — ordered by cost, filtered by cost.
func TestAttentionIsComposedFromTheSources(t *testing.T) {
	s := newStore(t)
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	seedLedger(t, s, 8, now.Add(-400*24*time.Hour)) // seq i at -400d + i minutes
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := s.DB().Exec(q, args...); err != nil {
			t.Fatalf("%v\n%s", err, q)
		}
	}
	// A year-old operational belief, never recalled: fading. A fresh one: not.
	addBelief(t, s, "b_old", "the old lighthouse is red", 3, 1)
	exec(`INSERT INTO ledger (seq, prev, ts, type, ring, payload, content) VALUES (99, '', ?, 'test.event', 3, '{}', '')`, now.Add(-time.Hour).UTC().Format(time.RFC3339Nano))
	exec(`INSERT INTO beliefs (id, statement, ring, confidence, evidence_count, first_seq, last_seq) VALUES ('b_new', 'the new lighthouse is white', 3, 0.8, 0, 99, 99)`)
	// A four-month-old belief with evidence (standard class) would be
	// fading unrecalled (0.5^(120/82) ≈ 0.36); ten conscious recalls slow
	// its clock and keep it up (≈ 0.63).
	exec(`INSERT INTO ledger (seq, prev, ts, type, ring, payload, content) VALUES (98, '', ?, 'test.event', 3, '{}', '')`, now.Add(-120*24*time.Hour).UTC().Format(time.RFC3339Nano))
	exec(`INSERT INTO beliefs (id, statement, ring, confidence, evidence_count, first_seq, last_seq) VALUES ('b_used', 'the tide table is on the wall', 3, 0.8, 1, 98, 98)`)
	exec(`INSERT INTO beliefs (id, statement, ring, confidence, evidence_count, first_seq, last_seq) VALUES ('b_unused', 'the tide table is by the door', 3, 0.8, 1, 98, 98)`)
	for i := 0; i < 10; i++ {
		if err := s.RecordMemoryAccess(now.Add(-time.Duration(i)*24*time.Hour), store.MemoryRef{Store: "beliefs", ID: "b_used"}); err != nil {
			t.Fatal(err)
		}
	}
	// An open tension, and an archived one that is no tension.
	exec(`INSERT INTO edges (id, from_id, to_id, edge_type, created_seq) VALUES ('ed_t', 'b_old', 'b_new', 'CONTRADICTS', 3)`)
	exec(`INSERT INTO edges (id, from_id, to_id, edge_type, created_seq, archived) VALUES ('ed_gone', 'b_new', 'b_used', 'CONTRADICTS', 4, 1)`)
	// An intention untouched for a year, one touched last week.
	exec(`INSERT INTO intentions (id, statement, state, created_seq, updated_seq) VALUES ('i_old', 'learn the tides', 'active', 5, 5)`)
	exec(`INSERT INTO intentions (id, statement, state, created_seq, updated_seq) VALUES ('i_new', 'paint the door', 'active', 6, 99)`)

	for i, id := range []string{"e1", "e2", "e3"} {
		addExperience(t, s, id, "an observation "+id, 6+i%2, now.Add(-time.Hour), false)
	}

	f := New(s)
	compose := func(want string) map[string][]AttentionItem {
		t.Helper()
		items, err := f.Attention(context.Background(), now, want)
		if err != nil {
			t.Fatal(err)
		}
		kinds := map[string][]AttentionItem{}
		for _, it := range items {
			if it.Cost != want {
				t.Fatalf("asked for %s, got %+v", want, it)
			}
			kinds[it.Kind] = append(kinds[it.Kind], it)
		}
		return kinds
	}
	low := compose(attention.CostLow)
	if got := low[attention.AttentionDecayAlert]; len(got) != 2 || got[0].ID != "b_old" || got[1].ID != "b_unused" || !strings.Contains(got[0].Text, "never recalled") {
		t.Fatalf("decay alerts = %+v, want b_old (a year unrecalled) then b_unused (four months, standard class); the fresh belief and the recalled one are not fading", got)
	}
	if got := low[attention.AttentionFollowup]; len(got) != 1 || got[0].ID != "i_old" {
		t.Fatalf("follow-ups = %+v, want the untouched intention alone", got)
	}
	if len(low) != 2 {
		t.Fatalf("low = %+v, want decay alerts and follow-ups only", low)
	}
	medium := compose(attention.CostMedium)
	if got := medium[attention.AttentionContradiction]; len(got) != 1 || got[0].ID != "ed_t" || !strings.Contains(got[0].Text, "old lighthouse") {
		t.Fatalf("contradictions = %+v, want the live edge alone", got)
	}
	if len(medium) != 1 {
		t.Fatalf("medium = %+v, want the tension only", medium)
	}
	if text := attention.RenderAttention(append(low[attention.AttentionDecayAlert], low[attention.AttentionFollowup]...)); !strings.HasPrefix(text, "- ") {
		t.Fatalf("rendering: %s", text)
	}

	// A composition reads only the sources of the cost it is asked for:
	// with the fading beliefs' source unreadable, the turn's medium item
	// still composes and the brief's low ones do not.
	exec(`DROP TABLE memory_access`)
	if _, err := f.Attention(context.Background(), now, attention.CostMedium); err != nil {
		t.Fatalf("the medium composition read the fading beliefs' source: %v", err)
	}
	if _, err := f.Attention(context.Background(), now, attention.CostLow); err == nil {
		t.Fatal("the low composition did not read the fading beliefs' source")
	}
}

// A tension is a CONTRADICTS edge nobody has resolved, and superseding or
// archiving either belief it joins resolves it. An open tension names only beliefs
// that still stand, so a retired belief never comes back to the turn as
// something to settle — whichever end of the edge it was.
func TestAResolvedTensionIsNotAttention(t *testing.T) {
	s := newStore(t)
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	seedLedger(t, s, 8, now.Add(-time.Hour))
	addBelief(t, s, "b_live", "the lighthouse lamp is lit at dusk", 3, 1)
	addBelief(t, s, "b_succ", "the lighthouse lamp is lit at sunset", 3, 2)
	addBelief(t, s, "b_sup", "the lighthouse lamp is lit at dawn", 3, 3)
	addBelief(t, s, "b_arch", "the lighthouse lamp is never lit", 3, 4)
	addExperience(t, s, "e_note", "the keeper lit the lamp at midnight", 5, now, false)
	for _, q := range []string{
		`UPDATE beliefs SET superseded_by = 'b_succ' WHERE id = 'b_sup'`,
		`UPDATE beliefs SET archived = 1 WHERE id = 'b_arch'`,
		`INSERT INTO edges (id, from_id, to_id, edge_type, created_seq) VALUES ('ed_open', 'e_note', 'b_live', 'CONTRADICTS', 6)`,
		`INSERT INTO edges (id, from_id, to_id, edge_type, created_seq) VALUES ('ed_to_superseded', 'e_note', 'b_sup', 'CONTRADICTS', 6)`,
		`INSERT INTO edges (id, from_id, to_id, edge_type, created_seq) VALUES ('ed_to_archived', 'e_note', 'b_arch', 'CONTRADICTS', 7)`,
		`INSERT INTO edges (id, from_id, to_id, edge_type, created_seq) VALUES ('ed_from_superseded', 'b_sup', 'b_live', 'CONTRADICTS', 7)`,
		`INSERT INTO edges (id, from_id, to_id, edge_type, created_seq) VALUES ('ed_from_archived', 'b_arch', 'b_succ', 'CONTRADICTS', 8)`,
	} {
		if _, err := s.DB().Exec(q); err != nil {
			t.Fatalf("%v\n%s", err, q)
		}
	}
	items, err := New(s).Attention(context.Background(), now, attention.CostMedium)
	if err != nil {
		t.Fatal(err)
	}
	var open []string
	for _, it := range items {
		if it.Kind == attention.AttentionContradiction {
			open = append(open, it.ID)
		}
	}
	if len(open) != 1 || open[0] != "ed_open" {
		t.Fatalf("open tensions = %v, want ed_open alone: a supersession or an archive on either end resolved the rest", open)
	}
}
