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
// .
// .
// .
// .
// .
// .
// .
// .
// .

// .
// .
func peerEvent(seq uint64, id string) *ledger.Event {
	b, _ := json.Marshal(map[string]interface{}{
		"id": id, "counterpart_name": "Peer", "counterpart_role": "peer",
		"relationship_type": "peer",
	})
	return &ledger.Event{Seq: seq, Type: ledger.EventRelationshipUpsert, Ring: 1,
		Timestamp: "2026-08-24T09:00:00Z", Payload: b}
}

// .
// .
// .
// .
func assertCharterStanding(t *testing.T, s *Store, wantID string) {
	t.Helper()
	assertOneCurrentOperator(t, s)
	cur, err := s.CurrentOperatorRelationship()
	if err != nil {
		t.Fatal(err)
	}
	if cur == nil {
		t.Fatal("no current operator relationship — the charter cannot reach the prompt")
	}
	if cur.ID != wantID || cur.CharterText == "" {
		t.Fatalf("the charter did not survive the refusal: %+v", cur)
	}
	text, err := s.CharterNarrative()
	if err != nil || text == "" {
		t.Fatalf("the charter narrative is empty after the refusal: %q (err %v)", text, err)
	}
}

func TestARelationshipCannotSupersedeItself(t *testing.T) {
	s := testStore(t)
	foundingOperator(t, s)

	err := s.Materialize(operatorEvent(2, "rel-founding", "rel-founding"))
	if err == nil {
		t.Fatal("a relationship superseding itself was accepted — no operator row is left unsuperseded")
	}
	if !strings.Contains(err.Error(), "rel-founding") {
		t.Fatalf("the refusal does not name the relationship: %v", err)
	}
	assertCharterStanding(t, s, "rel-founding")
}

// .
// .
// .
func TestASupersessionCycleCannotEmptyRing1(t *testing.T) {
	s := testStore(t)
	foundingOperator(t, s)
	if err := s.Materialize(operatorEvent(2, "rel-second", "rel-founding")); err != nil {
		t.Fatalf("a proper succession was refused: %v", err)
	}

	err := s.Materialize(operatorEvent(3, "rel-founding", "rel-second"))
	if err == nil {
		t.Fatal("a two-row supersession cycle was accepted — no operator row is left unsuperseded")
	}
	if !strings.Contains(err.Error(), "rel-founding") {
		t.Fatalf("the refusal does not name the relationship: %v", err)
	}
	assertCharterStanding(t, s, "rel-second")
}

// .
// .
// .
func TestAStoredPeerRowCannotEmptyRing1(t *testing.T) {
	s := testStore(t)
	if err := s.AddConversationTurn("operator", "Yes."); err != nil {
		t.Fatal(err)
	}
	if err := s.Materialize(peerEvent(1, "rel-peer")); err != nil {
		t.Fatal(err)
	}
	if err := s.Materialize(operatorEvent(2, "rel-founding", "")); err != nil {
		t.Fatalf("the founding operator relationship was refused: %v", err)
	}

	err := s.Materialize(operatorEvent(3, "rel-peer", "rel-founding"))
	if err == nil {
		t.Fatal("a stored peer row superseded the operator — no operator row is left unsuperseded")
	}
	if !strings.Contains(err.Error(), "rel-peer") {
		t.Fatalf("the refusal does not name the relationship: %v", err)
	}
	assertCharterStanding(t, s, "rel-founding")
}

// .
// .
// .
func TestSelfSupersessionIsRefusedInReplayToo(t *testing.T) {
	s := testStore(t)
	if err := s.MaterializeReplay(operatorEvent(1, "rel-founding", "")); err != nil {
		t.Fatal(err)
	}
	if err := s.MaterializeReplay(operatorEvent(2, "rel-founding", "rel-founding")); err == nil {
		t.Fatal("replay accepted a self-superseding row — a hand-crafted ledger could still orphan the charter")
	}
	assertOneCurrentOperator(t, s)
}

// .
// .
// .
func TestPreflightRefusesSelfSupersessionButAdmitsSuccession(t *testing.T) {
	s := testStore(t)
	foundingOperator(t, s)
	ring := legalRing(t, ledger.EventRelationshipUpsert)

	if err := s.ValidateEvent(ledger.EventRelationshipUpsert, ring,
		operatorEvent(2, "rel-founding", "rel-founding").Payload); err == nil {
		t.Fatal("the preflight would have signed a self-superseding relationship into the chain")
	}
	if err := s.ValidateEvent(ledger.EventRelationshipUpsert, ring,
		operatorEvent(2, "rel-second", "rel-founding").Payload); err != nil {
		t.Fatalf("the preflight refused a legitimate succession: %v", err)
	}
	assertCharterStanding(t, s, "rel-founding")
}
