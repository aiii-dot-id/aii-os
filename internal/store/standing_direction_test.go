package store

import (
	"encoding/json"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
)

func TestStandingCountsOnlyEvidencePointingAtTheBelief(t *testing.T) {
	s := testStore(t)
	var seq uint64
	mat := func(kind ledger.EventType, ring int, payload map[string]any) {
		t.Helper()
		seq++
		body, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Materialize(&ledger.Event{Seq: seq, Type: kind, Ring: ring,
			Timestamp: "2026-09-23T00:00:00Z", Payload: body}); err != nil {
			t.Fatalf("materialize %s: %v", kind, err)
		}
	}
	mat(ledger.EventRing0Genesis, 0, map[string]any{"name": "S"})
	for _, id := range []string{"claim", "recipient", "opposed"} {
		mat(ledger.EventBeliefUpsert, 3, map[string]any{
			"id": id, "statement": id, "ring": 3, "confidence": 0.5,
		})
	}
	if err := s.AddConversationTurn("operator", "I saw this"); err != nil {
		t.Fatal(err)
	}
	var turn uint64
	if err := s.db.QueryRow(`SELECT MAX(turn_seq) FROM conversations`).Scan(&turn); err != nil {
		t.Fatal(err)
	}
	mat(ledger.EventExperienceCreate, 3, map[string]any{
		"id": "self", "content": "my observation", "provenance": "self",
	})
	mat(ledger.EventExperienceCreate, 3, map[string]any{
		"id": "operator", "content": "their observation", "provenance": "operator", "source_turn": turn,
	})
	mat(ledger.EventEdgeCreate, 3, map[string]any{
		"id": "self_support", "from_id": "self", "to_id": "claim", "edge_type": "SUPPORTS",
	})
	mat(ledger.EventEdgeCreate, 3, map[string]any{
		"id": "operator_support", "from_id": "operator", "to_id": "claim", "edge_type": "SUPPORTS",
	})
	if got := mustStanding(t, s, "claim"); got != "new" {
		t.Fatalf("two incoming sources = %q, want new", got)
	}
	// The claim can be a source for another belief, but it cannot become
	// its own third independent source merely by pointing outward.
	mat(ledger.EventEdgeCreate, 3, map[string]any{
		"id": "claim_support", "from_id": "claim", "to_id": "recipient", "edge_type": "SUPPORTS",
	})
	if got := mustStanding(t, s, "claim"); got != "new" {
		t.Fatalf("outgoing support certified its own claim: %q", got)
	}
	// The same direction applies to contradiction: the target is suspect,
	// not the belief that names the contradiction.
	mat(ledger.EventEdgeCreate, 3, map[string]any{
		"id": "claim_opposes", "from_id": "claim", "to_id": "opposed", "edge_type": "CONTRADICTS",
	})
	if got := mustStanding(t, s, "claim"); got != "new" {
		t.Fatalf("outgoing contradiction made the source suspect: %q", got)
	}
	if got := mustStanding(t, s, "opposed"); got != "suspect" {
		t.Fatalf("incoming contradiction = %q, want suspect", got)
	}
	mat(ledger.EventExperienceCreate, 3, map[string]any{
		"id": "second_self", "content": "another observation", "provenance": "self",
	})
	mat(ledger.EventEdgeCreate, 3, map[string]any{
		"id": "second_support", "from_id": "second_self", "to_id": "claim", "edge_type": "SUPPORTS",
	})
	if got := mustStanding(t, s, "claim"); got != "confirmed" {
		t.Fatalf("three incoming sources = %q, want confirmed", got)
	}
}
