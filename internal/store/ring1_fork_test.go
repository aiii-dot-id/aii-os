package store

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
)

// Ring 1 is the charter — WHO the identity answers to. Two operator
// relationships with superseded_by NULL fork it, and
// CurrentOperatorRelationship breaks the tie with ORDER BY created_seq
// DESC: a sort deciding an authority question, silently.

func operatorEvent(seq uint64, id, supersedes string) *ledger.Event {
	payload := map[string]any{
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

// Naming a predecessor that was already superseded reached the same
// forked state: the old check proved the row was REAL, never CURRENT.
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

// Succession itself must keep working, and must move which row is
// current — the invariant is a guard rail, not a wall.
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
	// Both rows survive — succession is history, not deletion.
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM relationships`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("succession lost a row: %d", n)
	}
}

// Re-minting the SAME id is an update in place, not a second operator.
func TestReMintingTheSameRelationshipIsNotAFork(t *testing.T) {
	s := testStore(t)
	foundingOperator(t, s)
	if err := s.Materialize(operatorEvent(2, "rel-founding", "")); err != nil {
		t.Fatalf("updating the current operator relationship in place was refused: %v", err)
	}
	assertOneCurrentOperator(t, s)
}

// Mode-independent: relationships is derived from the ledger in ledger
// order, so which row is current is a pure function of the chain.
func TestTheForkIsRefusedInReplayToo(t *testing.T) {
	s := testStore(t)
	founding := operatorEvent(1, "rel-founding", "")
	if err := s.Materialize(founding); err != nil {
		t.Fatal(err)
	}
	if err := s.ReplayAll(EventSlice([]ledger.Event{*founding, *operatorEvent(2, "rel-second", "")})); err == nil {
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

// WHAT A RELATIONSHIP IS AFTER AN UPSERT IS READ FROM THE RELATIONSHIP
// (RelationshipAsHeld). It is Ring 1's by the rule
// CurrentOperatorRelationship itself uses: the unsuperseded operator's. A
// superseded operator's relationship keeps the role "operator" and is not
// Ring 1's; a peer's is not; an id nothing is held under answers nil. An
// entry naming "peer" under the current operator's id changes the name and
// leaves the role, and it is Ring 1's still: the operator's notice of a
// Ring 1 mint says whose relationship was written from this answer.
func TestARelationshipIsRing1sWhenItIsTheUnsupersededOperatorsWhateverAnEntryNames(t *testing.T) {
	s := testStore(t)
	foundingOperator(t, s)
	if err := s.Materialize(operatorEvent(2, "rel-second", "rel-founding")); err != nil {
		t.Fatal(err)
	}
	entry := func(seq uint64, id, name string) *ledger.Event {
		b, _ := json.Marshal(map[string]any{"id": id, "counterpart_name": name, "counterpart_role": "peer", "relationship_type": "peer", "charter_text": "we compare notes"})
		return &ledger.Event{Seq: seq, Type: ledger.EventRelationshipUpsert, Ring: 1, Timestamp: "2026-08-24T09:00:00Z", Payload: b}
	}
	if err := s.Materialize(entry(3, "rel-peer", "Ola")); err != nil {
		t.Fatal(err)
	}
	held := func(id, name, role string, ring1 bool) {
		t.Helper()
		rel, is, err := s.RelationshipAsHeld(id)
		if err != nil || rel == nil {
			t.Fatalf("%s: not read: %+v %v", id, rel, err)
		}
		if rel.ID != id || rel.CounterpartName != name || rel.CounterpartRole != role || is != ring1 {
			t.Errorf("%s is held as name %q, role %q, Ring 1's %v; want name %q, role %q, Ring 1's %v", id, rel.CounterpartName, rel.CounterpartRole, is, name, role, ring1)
		}
	}
	held("rel-second", "Op", "operator", true)
	held("rel-founding", "Op", "operator", false)
	held("rel-peer", "Ola", "peer", false)
	if rel, is, err := s.RelationshipAsHeld("rel-nobody"); rel != nil || is || err != nil {
		t.Errorf("an id nothing is held under: %+v, Ring 1's %v, %v; want none, false, no error", rel, is, err)
	}

	// The entry's own role does not decide it.
	if err := s.Materialize(entry(4, "rel-second", "Rue")); err != nil {
		t.Fatal(err)
	}
	held("rel-second", "Rue", "operator", true)
	if rel, _, err := s.RelationshipAsHeld("rel-second"); err != nil || rel.CharterText != "we compare notes" || rel.UpdatedSeq != 4 {
		t.Errorf("the relationship as the entry left it: %+v %v", rel, err)
	}
}
