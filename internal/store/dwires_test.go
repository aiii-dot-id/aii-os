package store

import "testing"

func plantLedger(t *testing.T, s *Store, upTo int) {
	t.Helper()
	for i := 1; i <= upTo; i++ {
		if _, err := s.db.Exec(`INSERT INTO ledger (seq, prev_hash, timestamp, type, author, ring, payload, content_hash, signature, sig_key_id)
			VALUES (?, 'h', '2026-08-26T00:00:00Z', 'x', 'test', 3, '{}', 'c', 's', 'k')`, i); err != nil {
			t.Fatal(err)
		}
	}
}

// The counters count CLAIMS by prefix — nothing else. NULL and empty
// outcomes (still-active intentions) stay out of every bucket.
func TestVerdictCountsTallyClaims(t *testing.T) {
	s := testStore(t)
	plantLedger(t, s, 5)
	rows := []struct{ id, state, outcome string }{
		{"i1", "completed", "served: shipped the fix"},
		{"i2", "completed", "served: answered"},
		{"i3", "completed", "partial: half the ask"},
		{"i4", "abandoned", "unserved: overtaken"},
		{"i5", "active", ""},
	}
	for _, r := range rows {
		if _, err := s.db.Exec(`INSERT INTO intentions (id, statement, state, outcome, created_seq) VALUES (?, 'st', ?, NULLIF(?, ''), 1)`,
			r.id, r.state, r.outcome); err != nil {
			t.Fatal(err)
		}
	}
	served, partial, unserved, err := s.VerdictCounts()
	if err != nil {
		t.Fatal(err)
	}
	if served != 2 || partial != 1 || unserved != 1 {
		t.Fatalf("counts %d/%d/%d, want 2/1/1", served, partial, unserved)
	}
}

// The nominator's pick: longest-untouched, ring-3, PLAIN — values,
// working_style, archived and superseded beliefs all have their own
// lifecycles and stay out.
func TestOldestStaleBeliefPicksAndFilters(t *testing.T) {
	s := testStore(t)
	plantLedger(t, s, 10)
	ins := func(id string, ring int, nodeType string, archived int, lastSeq int) {
		if _, err := s.db.Exec(`INSERT INTO beliefs (id, statement, ring, node_type, confidence, archived, first_seq, last_seq)
			VALUES (?, 'stmt '||?, ?, NULLIF(?, ''), 0.8, ?, 1, ?)`, id, id, ring, nodeType, archived, lastSeq); err != nil {
			t.Fatal(err)
		}
	}
	ins("b_old", 3, "", 0, 1)   // gap 9 — the pick
	ins("b_new", 3, "", 0, 8)   // gap 2
	ins("b_ring2", 2, "", 0, 1) // wrong ring
	ins("b_value", 3, "value", 0, 1)
	ins("b_arch", 3, "", 1, 1)

	sb, ok, err := s.OldestStaleBelief(5)
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if sb.ID != "b_old" || sb.Gap != 9 {
		t.Fatalf("picked %s gap %d, want b_old gap 9", sb.ID, sb.Gap)
	}
	if _, ok, _ := s.OldestStaleBelief(20); ok {
		t.Fatal("a gap floor above every belief still nominated one")
	}
}
