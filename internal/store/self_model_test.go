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
		var defaultValue any
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
	seqEvent(t, s, 1, ledger.EventBeliefUpsert, map[string]any{
		"id": "b_ring2", "statement": "Testing reveals truth", "ring": 3, "confidence": 0.8,
	})
	seqEvent(t, s, 2, ledger.EventExperienceCreate, map[string]any{
		"id": "x_ring2", "content": "The failure exposed the defect", "category": "observation",
	})
	seqEvent(t, s, 3, ledger.EventEdgeCreate, map[string]any{
		"id": "edge_ring2", "from_id": "x_ring2", "to_id": "b_ring2", "edge_type": "SUPPORTS",
	})
	seqEvent(t, s, 4, ledger.EventBeliefPromote, map[string]any{"id": "b_ring2", "ring": 2})

	id, err := s.PromptIdentity()
	if err != nil {
		t.Fatal(err)
	}
	material := id.Ring2
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
	seqEvent(t, s, 1, ledger.EventRelationshipUpsert, map[string]any{
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

// A portrait in the earlier wire shape is materialized as its own
// materializer did — refs as recorded, INSERT OR REPLACE by its recorded
// id — and that depends on the RECORD, not on the door it came through
// (T70): the one materializer reads it the same way whether it is called
// live or by replay. What keeps a new write from taking that shape is
// admission: ValidateEvent refuses it before anything is signed.
func TestARecordedPortraitReplacesByItsIDThroughTheOneMaterializer(t *testing.T) {
	first := probeEvent(1, ledger.EventSelfModelSynthesize, 3, map[string]any{"id": "prior", "content": "first recorded portrait", "source_entity_refs": []string{"old-reference"}})
	second := probeEvent(2, ledger.EventSelfModelSynthesize, 3, map[string]any{"id": "prior", "content": "replacement recorded portrait", "source_entity_refs": []string{"old-reference"}})

	s := testStore(t)
	if err := s.ValidateEvent(first.Type, first.Ring, first.Payload); err == nil {
		t.Fatal("admission took a new portrait in the earlier wire shape")
	}
	for _, evt := range []ledger.Event{first, second} {
		if err := s.Materialize(&evt); err != nil {
			t.Fatalf("the materializer refused a recorded portrait: %v", err)
		}
	}
	current, err := s.CurrentSelfModel()
	if err != nil || current == nil || current.ID != "prior" || current.CreatedSeq != 2 || current.SynthesisText != "replacement recorded portrait" {
		t.Fatalf("the recorded replacement did not replace by its id: %+v %v", current, err)
	}
	var rows int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM self_model_synthesis`).Scan(&rows); err != nil || rows != 1 {
		t.Fatalf("a replacement by recorded id left %d rows: %v", rows, err)
	}
}
