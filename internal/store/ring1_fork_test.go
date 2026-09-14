package store

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
)

// .
// .
// .
// .

func operatorEvent(seq uint64, id, supersedes string) *ledger.Event {
	payload := map[string]interface{}{
		"id": id, "counterpart_name": "Op", "counterpart_role": "operator",
		"relationship_type":         "founding_operator",
		"charter_text":              "we work together",
		"operator_approval_excerpt": "Yes — " + id,
		"operator_approval_turn":    float64(1),
		"approval_basis":            "conversation_turn",
	}
	if supersedes != "" {
		payload["supersedes"] = supersedes
	}
	b, _ := json.Marshal(payload)
	return &ledger.Event{Seq: seq, Type: ledger.EventRelationshipUpsert, Ring: 1,
		Timestamp: "2026-08-24T09:00:00Z", Payload: b}
}

func foundingOperator(t *testing.T, s *Store) {
	t.Helper()
	if err := s.AddConversationTurn("operator", "Yes."); err != nil {
		t.Fatal(err)
	}
	if err := s.Materialize(operatorEvent(1, "rel-founding", "")); err != nil {
		t.Fatalf("the founding operator relationship was refused: %v", err)
	}
}

func TestASecondOperatorRelationshipMustSupersedeTheFirst(t *testing.T) {
	s := testStore(t)
	foundingOperator(t, s)

	err := s.Materialize(operatorEvent(2, "rel-second", ""))
	if err == nil {
		t.Fatal("a second operator relationship naming no predecessor was accepted — Ring 1 is forked")
	}
	if !strings.Contains(err.Error(), "rel-founding") {
		t.Fatalf("the refusal does not name the relationship that must be superseded: %v", err)
	}
	assertOneCurrentOperator(t, s)
}

// .
// .
func TestSupersedingAnAlreadySupersededRowIsRefused(t *testing.T) {
	s := testStore(t)
	foundingOperator(t, s)
	if err := s.Materialize(operatorEvent(2, "rel-second", "rel-founding")); err != nil {
		t.Fatalf("a proper succession was refused: %v", err)
	}

	err := s.Materialize(operatorEvent(3, "rel-third", "rel-founding"))
	if err == nil {
		t.Fatal("superseding an already-superseded row was accepted — Ring 1 is forked")
	}
	if !strings.Contains(err.Error(), "rel-second") {
		t.Fatalf("the refusal does not name the actually-current relationship: %v", err)
	}
	assertOneCurrentOperator(t, s)
}

// .
// .
func TestSuccessionStillReplacesTheCurrentOperator(t *testing.T) {
	s := testStore(t)
	foundingOperator(t, s)
	if err := s.Materialize(operatorEvent(2, "rel-second", "rel-founding")); err != nil {
		t.Fatal(err)
	}
	assertOneCurrentOperator(t, s)
	cur, err := s.CurrentOperatorRelationship()
	if err != nil || cur == nil {
		t.Fatalf("no current operator relationship after succession: %v", err)
	}
	if cur.ID != "rel-second" {
		t.Fatalf("succession did not move Ring 1: current is %q", cur.ID)
	}
	// .
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM relationships`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("succession lost a row: %d", n)
	}
}

// .
func TestReMintingTheSameRelationshipIsNotAFork(t *testing.T) {
	s := testStore(t)
	foundingOperator(t, s)
	if err := s.Materialize(operatorEvent(2, "rel-founding", "")); err != nil {
		t.Fatalf("updating the current operator relationship in place was refused: %v", err)
	}
	assertOneCurrentOperator(t, s)
}

// .
// .
func TestTheForkIsRefusedInReplayToo(t *testing.T) {
	s := testStore(t)
	if err := s.MaterializeReplay(operatorEvent(1, "rel-founding", "")); err != nil {
		t.Fatal(err)
	}
	if err := s.MaterializeReplay(operatorEvent(2, "rel-second", "")); err == nil {
		t.Fatal("replay accepted a fork that live mode refuses — invariants are mode-independent")
	}
	assertOneCurrentOperator(t, s)
}

func assertOneCurrentOperator(t *testing.T, s *Store) {
	t.Helper()
	var n int
	if err := s.db.QueryRow(
		`SELECT COUNT(*) FROM relationships WHERE counterpart_role = 'operator' AND superseded_by IS NULL`,
	).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("%d current operator relationships — the identity answers to whichever sorts last", n)
	}
}
