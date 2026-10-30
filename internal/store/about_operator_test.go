package store

import (
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
)

// operatorRelationship materializes Ring 1's relationship rel with the
// operator's affirmation, at seq; supersedes names the one it succeeds.
func operatorRelationship(t *testing.T, s *Store, seq uint64, rel, name, relType, supersedes string) {
	t.Helper()
	if err := s.AddConversationTurn("operator", "Yes — approved."); err != nil {
		t.Fatal(err)
	}
	turn, err := s.GetLatestOperatorTurn()
	if err != nil || turn == nil {
		t.Fatalf("operator turn = %+v, %v", turn, err)
	}
	payload := map[string]any{
		"id": rel, "counterpart_name": name, "counterpart_role": "operator",
		"relationship_type": relType, "charter_text": "CHARTER-TEXT " + name + " and I build carefully.",
		"operator_approval_excerpt": turn.Content, "operator_approval_turn": turn.TurnSeq,
		"approval_basis": "conversation_turn",
	}
	if supersedes != "" {
		payload["supersedes"] = supersedes
	}
	seqEvent(t, s, seq, ledger.EventRelationshipUpsert, payload)
}

func TestTheBeliefsAboutTheOperatorAreTheOnesRing1sRelationshipShaped(t *testing.T) {
	s := testStore(t)
	operatorRelationship(t, s, 1, "rel_quinn", "Quinn", "founding_operator", "")
	belief := func(seq uint64, id, statement string) {
		seqEvent(t, s, seq, ledger.EventBeliefUpsert, map[string]any{"id": id, "statement": statement, "ring": 3, "confidence": 0.7})
	}
	edge := func(seq uint64, id, from, to, typ string) {
		seqEvent(t, s, seq, ledger.EventEdgeCreate, map[string]any{"id": id, "from_id": from, "to_id": to, "edge_type": typ})
	}
	belief(2, "b_asks", "Quinn asks what you think")
	belief(3, "b_plain", "Quinn corrects plainly")
	belief(4, "b_other", "Corrections are cheap")
	belief(5, "b_old", "Quinn prefers long answers")
	belief(6, "b_kept", "Quinn values precision")
	edge(7, "e1", "rel_quinn", "b_asks", "SHAPED_BY")
	edge(8, "e2", "rel_quinn", "b_plain", "SHAPED_BY")
	edge(9, "e3", "rel_quinn", "b_old", "SHAPED_BY")
	edge(10, "e4", "rel_quinn", "b_kept", "SHAPED_BY")
	seqEvent(t, s, 11, ledger.EventExperienceCreate, map[string]any{"id": "x1", "content": "an experience", "category": "observation"})
	edge(12, "e5", "x1", "b_other", "SUPPORTS")
	seqEvent(t, s, 13, ledger.EventBeliefSupersede, map[string]any{"old_id": "b_old", "new_id": "b_asks", "reason": "revised"})
	seqEvent(t, s, 14, ledger.EventBeliefPromote, map[string]any{"id": "b_kept", "ring": 2})
	// Evidence added to b_plain after b_asks: it is the more recent.
	edge(15, "e6", "x1", "b_plain", "SUPPORTS")

	about, err := s.AboutOperator()
	if err != nil {
		t.Fatal(err)
	}
	if about.RelationshipID != "rel_quinn" || about.Name != "Quinn" {
		t.Fatalf("the relationship is not Ring 1's: %+v", about)
	}
	var ids []string
	for _, b := range about.Beliefs {
		ids = append(ids, b.ID)
	}
	if len(ids) != 2 || ids[0] != "b_plain" || ids[1] != "b_asks" {
		t.Fatalf("THE BELIEFS ABOUT THE OPERATOR ARE NOT THE STANDING RING 3 ONES THE RELATIONSHIP SHAPED, MOST RECENT FIRST: %v", ids)
	}
	identity, err := s.PromptIdentity()
	if err != nil {
		t.Fatal(err)
	}
	if len(identity.AboutOperator) != 2 || identity.AboutOperator[0] != about.Beliefs[0] || identity.AboutOperator[1] != about.Beliefs[1] {
		t.Fatalf("the prompt's identity does not carry them: %+v", identity.AboutOperator)
	}

	// The mark is not evidence: standing does not count it.
	var count int
	if err := s.db.QueryRow(`SELECT evidence_count FROM beliefs WHERE id = 'b_asks'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("the mark counted as evidence: evidence_count=%d %v", count, err)
	}

	// A promoted belief about them names the relationship in Ring 2, and
	// never quotes the charter Ring 1 renders.
	if len(identity.Ring2) != 1 || len(identity.Ring2[0].Evidence) != 1 {
		t.Fatalf("Ring 2 = %+v", identity.Ring2)
	}
	if got := identity.Ring2[0].Evidence[0].Content; got != "your relationship with Quinn" {
		t.Fatalf("A RELATIONSHIP THAT SHAPED A RING 2 BELIEF IS RENDERED AS %q, not by its name", got)
	}

	// A successor operator does not inherit what was learned of the one
	// before.
	operatorRelationship(t, s, 16, "rel_robin", "Robin", "operator", "rel_quinn")
	if about, err = s.AboutOperator(); err != nil || about.RelationshipID != "rel_robin" || len(about.Beliefs) != 0 {
		t.Fatalf("the beliefs about the operator before followed to their successor: %+v %v", about, err)
	}
}

func TestWithNoOperatorRelationshipNoBeliefIsAboutTheOperator(t *testing.T) {
	s := testStore(t)
	seqEvent(t, s, 1, ledger.EventBeliefUpsert, map[string]any{"id": "b1", "statement": "Someone asks first", "ring": 3, "confidence": 0.7})
	about, err := s.AboutOperator()
	if err != nil || about.RelationshipID != "" || len(about.Beliefs) != 0 {
		t.Fatalf("about = %+v, %v", about, err)
	}
}
