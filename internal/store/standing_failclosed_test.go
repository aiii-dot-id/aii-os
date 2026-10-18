package store

import (
	"encoding/json"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
)

func TestReviewUnknownEvidenceExistenceCannotConfirmRing2Standing(t *testing.T) {
	s := testStore(t)
	seq := uint64(0)
	mat := func(et ledger.EventType, payload map[string]any) {
		seq++
		b, _ := json.Marshal(payload)
		if err := s.Materialize(&ledger.Event{Seq: seq, Type: et, Ring: 3, Timestamp: "2026-09-02T00:00:00Z", Payload: b}); err != nil {
			t.Fatalf("materialize %s: %v", et, err)
		}
	}
	mat(ledger.EventRing0Genesis, map[string]any{"name": "S"})
	mat(ledger.EventBeliefUpsert, map[string]any{"id": "b", "statement": "claim", "ring": 3, "confidence": 0.5})
	if err := s.AddConversationTurn("operator", "observed"); err != nil {
		t.Fatal(err)
	}
	var turn uint64
	if err := s.db.QueryRow(`SELECT MAX(turn_seq) FROM conversations`).Scan(&turn); err != nil {
		t.Fatal(err)
	}
	mat(ledger.EventExperienceCreate, map[string]any{"id": "self", "content": "self evidence", "provenance": "self"})
	mat(ledger.EventExperienceCreate, map[string]any{"id": "operator", "content": "operator evidence", "provenance": "operator", "source_turn": turn})
	mat(ledger.EventEdgeCreate, map[string]any{"id": "e_self", "from_id": "self", "to_id": "b", "edge_type": "SUPPORTS"})
	mat(ledger.EventEdgeCreate, map[string]any{"id": "e_operator", "from_id": "operator", "to_id": "b", "edge_type": "SUPPORTS"})
	// Plant old/corrupt graph state that the current live gate would refuse,
	// then make EntityExists fail only while resolving that ghost. Standing
	// is an authority gate, so unknown must not count as existing evidence.
	if _, err := s.db.Exec(`INSERT INTO edges(id,from_id,to_id,edge_type,created_seq) VALUES('e_ghost','ghost','b','SUPPORTS',?)`, seq); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`ALTER TABLE experiences RENAME TO experiences_unreadable`); err != nil {
		t.Fatal(err)
	}

	got, err := s.StandingFor("b")
	if err == nil && (got == "confirmed" || got == "trusted") {
		t.Fatalf("unknown evidence existence certified Ring 2 standing: got %q, want an error (or new)", got)
	}
	// And fail-closed means it is an ERROR, not a standing of any kind.
	if err == nil {
		t.Fatalf("unknown evidence existence produced a standing %q instead of an error", got)
	}
}
