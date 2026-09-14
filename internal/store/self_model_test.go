package store

import (
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
)

func TestSelfModelProjectionHasNoVerificationLifecycle(t *testing.T) {
	s := testStore(t)
	rows, err := s.db.Query(`PRAGMA table_info(self_model_synthesis)`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull, pk int
		var defaultValue interface{}
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
			t.Fatal(err)
		}
		if name == "verification_status" {
			t.Fatal("accepted-only projection must not contain verification_status")
		}
	}
}

func TestRing2MaterialDerivesPromotedBeliefAndEvidence(t *testing.T) {
	s := testStore(t)
	seqEvent(t, s, 1, ledger.EventBeliefUpsert, map[string]interface{}{
		"id": "b_ring2", "statement": "Testing reveals truth", "ring": 3, "confidence": 0.8,
	})
	seqEvent(t, s, 2, ledger.EventExperienceCreate, map[string]interface{}{
		"id": "x_ring2", "content": "The failure exposed the defect", "category": "observation",
	})
	seqEvent(t, s, 3, ledger.EventEdgeCreate, map[string]interface{}{
		"id": "edge_ring2", "from_id": "x_ring2", "to_id": "b_ring2", "edge_type": "SUPPORTS",
	})
	seqEvent(t, s, 4, ledger.EventBeliefPromote, map[string]interface{}{"id": "b_ring2", "ring": 2})

	material, err := s.Ring2Material()
	if err != nil {
		t.Fatal(err)
	}
	if len(material) != 1 || len(material[0].Evidence) != 1 {
		t.Fatalf("Ring 2 material = %+v", material)
	}
	if material[0].Evidence[0].Content != "The failure exposed the defect" || material[0].Evidence[0].Provenance != "self" {
		t.Fatalf("Ring 2 evidence = %+v", material[0].Evidence[0])
	}
	snapshot, err := s.PromptIdentity()
	if err != nil || len(snapshot.Ring2) != 1 || snapshot.SelfModel != nil || snapshot.Charter != "" {
		t.Fatalf("prompt identity snapshot = %+v err=%v", snapshot, err)
	}
}

func TestPromptIdentityDerivesRing1Presence(t *testing.T) {
	s := testStore(t)
	before, err := s.PromptIdentity()
	if err != nil {
		t.Fatal(err)
	}
	if before.HasOperatorRelationship {
		t.Fatal("new identity projection must not claim Ring 1")
	}

	if err := s.AddConversationTurn("operator", "Yes — rel_prompt_ring1 approved."); err != nil {
		t.Fatal(err)
	}
	turn, err := s.GetLatestOperatorTurn()
	if err != nil || turn == nil {
		t.Fatalf("operator turn = %+v, %v", turn, err)
	}
	seqEvent(t, s, 1, ledger.EventRelationshipUpsert, map[string]interface{}{
		"id": "rel_prompt_ring1", "counterpart_name": "Operator", "counterpart_role": "operator",
		"relationship_type": "founding_operator", "charter_text": "We choose direct collaboration.",
		"operator_approval_excerpt": turn.Content, "operator_approval_turn": turn.TurnSeq,
		"approval_basis": "conversation_turn",
	})

	after, err := s.PromptIdentity()
	if err != nil {
		t.Fatal(err)
	}
	if !after.HasOperatorRelationship || after.Charter != "We choose direct collaboration." {
		t.Fatalf("Ring 1 projection = %+v", after)
	}
}
