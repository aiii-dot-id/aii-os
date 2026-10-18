package store

// StandingReportFor observes the StandingFor derivation into a structured
// report: the same branches (outending, contradiction, ghost, wrong-type),
// the same counts, the same fail-closed tolerance. This test pins the
// observation to the branches' own reasons, not a second algorithm.

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
)

func reportStore(t *testing.T) (*Store, func(ledger.EventType, int, map[string]any)) {
	t.Helper()
	s := testStore(t)
	var seq uint64
	mat := func(kind ledger.EventType, ring int, payload map[string]any) {
		t.Helper()
		seg := seq + 1
		body, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Materialize(&ledger.Event{Seq: seg, Type: kind, Ring: ring,
			Timestamp: "2026-09-23T00:00:00Z", Payload: body}); err != nil {
			t.Fatalf("materialize %s: %v", kind, err)
		}
		seq = seg
	}
	mat(ledger.EventRing0Genesis, 0, map[string]any{"name": "S"})
	for _, id := range []string{"claim", "other"} {
		mat(ledger.EventBeliefUpsert, 3, map[string]any{
			"id": id, "statement": id, "ring": 3, "confidence": 0.5,
		})
	}
	mat(ledger.EventExperienceCreate, 3, map[string]any{
		"id": "exp", "content": "an observation", "provenance": "self",
	})
	mat(ledger.EventExperienceCreate, 3, map[string]any{
		"id": "exp2", "content": "another observation", "provenance": "self",
	})
	mat(ledger.EventExperienceCreate, 3, map[string]any{
		"id": "exp3", "content": "a third observation", "provenance": "self",
	})
	mat(ledger.EventExperienceCreate, 3, map[string]any{
		"id": "exp4", "content": "a fourth observation", "provenance": "self",
	})
	return s, mat
}

func TestStandingReportClassifiesEveryBranchWithItsReason(t *testing.T) {
	s, mat := reportStore(t)
	mat(ledger.EventEdgeCreate, 3, map[string]any{
		"id": "in1", "from_id": "exp", "to_id": "claim", "edge_type": "SUPPORTS",
	})
	mat(ledger.EventEdgeCreate, 3, map[string]any{
		"id": "in2", "from_id": "exp2", "to_id": "claim", "edge_type": "SUPPORTS",
	})
	// Outending: the claim points at the other belief — evidence for the
	// other end, excluded for its own.
	mat(ledger.EventEdgeCreate, 3, map[string]any{
		"id": "out", "from_id": "claim", "to_id": "other", "edge_type": "SUPPORTS",
	})
	// Wrong type: an incoming edge the ladder never counts. SHAPED_BY is
	// schema-valid but outside SUPPORTS/REINFORCED_BY/DERIVED_FROM, and it
	// must come from a fresh source — the store dedupes edge.create by
	// (from_id, to_id, edge_type) and refuses ledgered no-ops.
	mat(ledger.EventEdgeCreate, 3, map[string]any{
		"id": "wrong", "from_id": "exp3", "to_id": "claim", "edge_type": "SHAPED_BY",
	})
	// Ghost: the source does not resolve as evidence. created_seq must
	// reference a real ledger event (FK), so the genesis seq stands in.
	if _, err := s.db.Exec(`INSERT INTO edges(id,from_id,to_id,edge_type,created_seq) VALUES('ghost','nobody','claim','SUPPORTS',1)`); err != nil {
		t.Fatal(err)
	}
	// Contradiction: an incoming CONTRADICTS marks the belief suspect.
	mat(ledger.EventEdgeCreate, 3, map[string]any{
		"id": "contra", "from_id": "exp4", "to_id": "claim", "edge_type": "CONTRADICTS",
	})

	r, err := s.StandingDetail(context.Background(), "claim", &StandingCursor{})
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	if len(r.Qualifying) != 2 {
		t.Fatalf("qualifying = %d, want 2 (the two resolving SUPPORTS)", len(r.Qualifying))
	}
	if r.DistinctSources != 2 {
		t.Fatalf("distinct sources = %d, want 2", r.DistinctSources)
	}
	if !r.Contradicted {
		t.Fatal("incoming CONTRADICTS did not mark the belief Contradicted")
	}
	if r.Standing != "suspect" || r.Eligible {
		t.Fatalf("contradicted belief = %q eligible=%v, want suspect not eligible", r.Standing, r.Eligible)
	}
	reasons := map[string]string{}
	for _, x := range r.Excluded {
		reasons[x.Edge.ID] = x.Reason
	}
	if reasons["out"] != "outending direction" {
		t.Fatalf("outending edge reason = %q, want outending direction", reasons["out"])
	}
	if reasons["ghost"] != "ghost source" {
		t.Fatalf("ghost edge reason = %q, want ghost source", reasons["ghost"])
	}
	if reasons["wrong"] != "edge type not counted" {
		t.Fatalf("wrong-type edge reason = %q, want edge type not counted", reasons["wrong"])
	}
	if reasons["contra"] != "contradiction" {
		t.Fatalf("contradiction edge reason = %q, want contradiction", reasons["contra"])
	}
	if r.Retired {
		t.Fatal("live belief reported retired")
	}
	if r.Belief.ID != "claim" {
		t.Fatalf("belief row = %q, want claim", r.Belief.ID)
	}
	if r.ReadHeadSeq <= 0 {
		t.Fatalf("read-head seq = %d, want the live head", r.ReadHeadSeq)
	}
	// The report is the derivation: the wrapper and the report must agree.
	if got := mustStanding(t, s, "claim"); got != r.Standing {
		t.Fatalf("StandingFor = %q, report = %q — the wrapper diverged", got, r.Standing)
	}
}

func TestStandingReportFailsClosedNotAsStanding(t *testing.T) {
	s, mat := reportStore(t)
	mat(ledger.EventEdgeCreate, 3, map[string]any{
		"id": "in1", "from_id": "exp", "to_id": "claim", "edge_type": "SUPPORTS",
	})
	if _, err := s.db.Exec(`ALTER TABLE experiences RENAME TO experiences_unreadable`); err != nil {
		t.Fatal(err)
	}
	r, err := s.StandingReportFor("claim")
	// Fail-closed is an error, and the caller decides how to say so —
	// never a standing, never eligibility.
	if err == nil {
		t.Fatalf("unreadable evidence produced a report %+v instead of an error", r)
	}
	if r != nil && (r.Standing == "confirmed" || r.Standing == "trusted") {
		t.Fatalf("unreadable evidence certified standing %q", r.Standing)
	}
}
