package app

// THE TWO PROOFS CODEX'S ITEM 6 STILL LACKED (host 1530 item 4).
//
// The first is the replay comparison: the record part of the trail is
// derived from the ledger, while the recall measure beside it is a
// reading of memory_access, which is ephemeral. Those two must be
// separable. A trail whose record moved because a recall count moved
// would mean the page's view of what a belief rests on is a function
// of how often it happened to be read, which is not what the record
// says.
//
// The second is per-arm independence. The bound is per arm, so fifty
// supports must not crowd out a contradiction. A page that shows "50
// origins, nothing else" on a contradicted belief is worse than one
// that shows a truncated list and says so.

import (
	"reflect"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
)

// stripEphemeral returns the trail with the recall reading removed, so
// two answers can be compared for everything except the measure that is
// allowed to move between them.
func stripEphemeral(tr *dashboard.BeliefTrail) dashboard.BeliefTrail {
	c := *tr
	c.Belief.Recall = nil
	c.Belief.RecallCount = nil
	c.Belief.RecalledAt = nil
	c.Belief.Fading = false
	return c
}

// The record part of the trail is the same answer twice; only the
// ephemeral recall measure moves. If this fails, the page's account of
// what a belief rests on depends on how often it was recalled.
func TestBeliefTrailRecordIsStableWhileRecallMoves(t *testing.T) {
	a, st := trailApp(t)
	db := st.DB()

	seedLedger(t, st, 1, at(10))
	seedLedger(t, st, 2, at(5))
	if _, err := db.Exec(
		`INSERT INTO beliefs (id, statement, ring, confidence, evidence_count,
		                      archived, first_seq, last_seq)
		 VALUES ('b_stable', 'a record read is not a recall read', 3, 0.7, 2, 0, 1, 2)`); err != nil {
		t.Fatalf("seed belief: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO experiences (id, content, category, provenance, created_seq, created_at)
		 VALUES ('e_stable', 'the gate refused', 'observation', 'self', 2, ?)`, at(5)); err != nil {
		t.Fatalf("seed experience: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO edges (id, from_id, to_id, edge_type, strength, created_seq)
		 VALUES ('ed_stable', 'e_stable', 'b_stable', 'SUPPORTS', 1.0, 2)`); err != nil {
		t.Fatalf("seed edge: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO memory_access (store, id, count, last_at, history)
		 VALUES ('beliefs', 'b_stable', 2, ?, '[]')`, at(1)); err != nil {
		t.Fatalf("seed access: %v", err)
	}

	first, err := a.beliefTrailForDashboard("b_stable")
	if err != nil {
		t.Fatalf("first read: %v", err)
	}

	// Only the ephemeral measure changes. Nothing in the record moves.
	if _, err := db.Exec(
		`UPDATE memory_access SET count = 9, last_at = ? WHERE store = 'beliefs' AND id = 'b_stable'`,
		at(0)); err != nil {
		t.Fatalf("move the recall: %v", err)
	}

	second, err := a.beliefTrailForDashboard("b_stable")
	if err != nil {
		t.Fatalf("second read: %v", err)
	}

	if !reflect.DeepEqual(stripEphemeral(first), stripEphemeral(second)) {
		t.Errorf("the record part of the trail moved when only the recall measure changed:\nfirst:  %+v\nsecond: %+v",
			stripEphemeral(first), stripEphemeral(second))
	}

	// And the measure genuinely moved, or the comparison above would
	// pass for a trail that never reads memory_access at all.
	if first.Belief.RecallCount == nil || second.Belief.RecallCount == nil {
		t.Fatalf("recall_count is null on one of the two reads, so nothing was measured")
	}
	if *first.Belief.RecallCount == *second.Belief.RecallCount {
		t.Errorf("recall_count = %d on both reads, want the second to see the moved count %d",
			*second.Belief.RecallCount, 9)
	}
	if first.Belief.Recall == nil || second.Belief.Recall == nil {
		t.Fatalf("recall strength is null on one of the two reads, so nothing was measured")
	}
	if *first.Belief.Recall == *second.Belief.Recall {
		t.Errorf("recall strength = %v on both reads, want the second to reflect more accesses",
			*second.Belief.Recall)
	}
}

// The bound is per arm. A belief with more supports than the bound and
// one contradiction must still show the contradiction: the page needs
// to know it is contradicted even when the supports were cut.
func TestBeliefTrailBoundsArmsIndependently(t *testing.T) {
	a, st := trailApp(t)
	db := st.DB()

	seedLedger(t, st, 1, at(10))
	seedLedger(t, st, 2, at(5))
	if _, err := db.Exec(
		`INSERT INTO beliefs (id, statement, ring, confidence, evidence_count,
		                      archived, first_seq, last_seq)
		 VALUES ('b_crowded', 'many supports, one contradiction', 3, 0.7, 3, 0, 1, 2)`); err != nil {
		t.Fatalf("seed belief: %v", err)
	}

	// More supports than the bound, so the origins arm must be cut.
	for i := 0; i < trailLinkLimit+5; i++ {
		id := "e_c" + string(rune('a'+i%26)) + string(rune('a'+i/26))
		if _, err := db.Exec(
			`INSERT INTO experiences (id, content, category, provenance, created_seq, created_at)
			 VALUES (?, 'support', 'observation', 'self', 2, ?)`, id, at(5)); err != nil {
			t.Fatalf("seed experience %d: %v", i, err)
		}
		if _, err := db.Exec(
			`INSERT INTO edges (id, from_id, to_id, edge_type, strength, created_seq)
			 VALUES (?, ?, 'b_crowded', 'SUPPORTS', 1.0, 2)`, "ed_"+id, id); err != nil {
			t.Fatalf("seed edge %d: %v", i, err)
		}
	}

	// One contradiction, well inside its own arm's bound.
	if _, err := db.Exec(
		`INSERT INTO experiences (id, content, category, provenance, created_seq, created_at)
		 VALUES ('e_against', 'this belief is wrong', 'observation', 'self', 2, ?)`, at(5)); err != nil {
		t.Fatalf("seed contradicting experience: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO edges (id, from_id, to_id, edge_type, strength, created_seq)
		 VALUES ('ed_against', 'e_against', 'b_crowded', 'CONTRADICTS', 1.0, 2)`); err != nil {
		t.Fatalf("seed contradicting edge: %v", err)
	}

	trail, err := a.beliefTrailForDashboard("b_crowded")
	if err != nil {
		t.Fatalf("beliefTrailForDashboard(b_crowded): %v", err)
	}

	if len(trail.Origins) != trailLinkLimit {
		t.Errorf("origins = %d, want the bound %d", len(trail.Origins), trailLinkLimit)
	}
	if !trail.Truncated.Origins {
		t.Error("truncated.origins = false, want true: the arm was cut")
	}

	// The contradiction is inside its bound and must survive the cut
	// applied to a different arm.
	if len(trail.Contradicts) != 1 {
		t.Fatalf("contradicts = %d, want 1: a crowded origins arm must not swallow the contradiction",
			len(trail.Contradicts))
	}
	if trail.Contradicts[0].ID != "e_against" {
		t.Errorf("contradicts[0].ID = %q, want e_against", trail.Contradicts[0].ID)
	}
	if trail.Truncated.Contradicts {
		t.Error("truncated.contradicts = true, want false: that arm was not cut")
	}
}
