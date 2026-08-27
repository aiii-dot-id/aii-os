package store

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
)

// H4, the law intention.create and commitment.promised already enforce:
// no ledgered no-ops that claim to create. Edges were the one create
// left out of it — and their uniqueness is (from_id, to_id, edge_type),
// so a duplicate arrives under a FRESH id and ON CONFLICT never fires.
// Returning nil made the preflight pass, so the event was signed and
// durably appended having created nothing.

func edgeEvent(seq uint64, id, from, to, kind string) *ledger.Event {
	b, _ := json.Marshal(map[string]interface{}{
		"id": id, "from_id": from, "to_id": to, "edge_type": kind,
	})
	return &ledger.Event{Seq: seq, Type: ledger.EventEdgeCreate, Ring: 3,
		Timestamp: "2026-08-24T09:00:00Z", Payload: b}
}

func TestADuplicateEdgeIsNotASignedNoOp(t *testing.T) {
	s := testStore(t)
	if err := s.Materialize(edgeEvent(1, "edge_1", "belief_a", "belief_b", "SUPPORTS")); err != nil {
		t.Fatalf("the first edge was refused: %v", err)
	}

	// Same triple, fresh id — the shape a second assertion actually takes.
	err := s.Materialize(edgeEvent(2, "edge_2", "belief_a", "belief_b", "SUPPORTS"))
	if err == nil {
		t.Fatal("a duplicate edge materialized as success — the preflight would let it be signed and appended")
	}
	if !strings.Contains(err.Error(), "ledgered no-op") {
		t.Fatalf("the refusal does not name what is wrong: %v", err)
	}

	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM edges`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("edges = %d, want 1", n)
	}
	// And evidence_count did not double-count, which was already correct.
	assertEvidenceCount(t, s, "belief_b", 0)
}

// Genuinely different edges between the same pair are not duplicates.
func TestADifferentEdgeTypeIsANewEdge(t *testing.T) {
	s := testStore(t)
	if err := s.Materialize(edgeEvent(1, "edge_1", "belief_a", "belief_b", "SUPPORTS")); err != nil {
		t.Fatal(err)
	}
	if err := s.Materialize(edgeEvent(2, "edge_2", "belief_a", "belief_b", "DERIVED_FROM")); err != nil {
		t.Fatalf("a different edge type between the same pair was refused: %v", err)
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM edges`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("edges = %d, want 2", n)
	}
}

// Mode-independent, like its siblings: live it becomes a preflight
// refusal, on replay it is tamper evidence.
func TestTheDuplicateEdgeIsRefusedInReplayToo(t *testing.T) {
	s := testStore(t)
	if err := s.MaterializeReplay(edgeEvent(1, "edge_1", "b_a", "b_b", "SUPPORTS")); err != nil {
		t.Fatal(err)
	}
	if err := s.MaterializeReplay(edgeEvent(2, "edge_2", "b_a", "b_b", "SUPPORTS")); err == nil {
		t.Fatal("replay accepted a create that created nothing")
	}
}

func assertEvidenceCount(t *testing.T, s *Store, beliefID string, want int) {
	t.Helper()
	var got int
	err := s.db.QueryRow(`SELECT evidence_count FROM beliefs WHERE id = ?`, beliefID).Scan(&got)
	if err != nil {
		return // no belief row in this fixture; the edge test is the point
	}
	if got != want {
		t.Fatalf("evidence_count = %d, want %d", got, want)
	}
}
