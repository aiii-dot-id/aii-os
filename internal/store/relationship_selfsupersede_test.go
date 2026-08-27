package store

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
)

// The ZERO-row fork (B5): Ring 1 is the unsuperseded operator row, and
// three different upserts passed every payload gate and then left NONE.
// A row superseding ITSELF (the at-most-one-current probe excludes the
// incoming id, so the event read as "founding operator, nothing to
// supersede"); a two-row CYCLE, where an already-superseded row is
// re-minted naming its own successor; and a stored PEER row re-minted
// with an operator payload, which validates as an operator and then
// supersedes one while ON CONFLICT leaves counterpart_role alone. Each
// left the charter the prompt reads with no row to come from, and
// nothing on the mint path inspects `supersedes`, so one hallucinated
// field in a charter-update conversation was enough. The invariant is
// therefore about the PROJECTION, not the payload: after an operator
// write, exactly one current operator relationship must remain.

// peerEvent is the non-operator counterpart of operatorEvent: no R52
// evidence, because Ring 1 does not govern peers.
func peerEvent(seq uint64, id string) *ledger.Event {
	b, _ := json.Marshal(map[string]interface{}{
		"id": id, "counterpart_name": "Peer", "counterpart_role": "peer",
		"relationship_type": "peer",
	})
	return &ledger.Event{Seq: seq, Type: ledger.EventRelationshipUpsert, Ring: 1,
		Timestamp: "2026-08-24T09:00:00Z", Payload: b}
}

// assertCharterStanding is the property every refusal here must leave
// behind: Ring 1 still names exactly one operator, and that operator's
// charter still reaches the prompt. A refusal that lost the charter
// anyway would be no fix.
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

// The two-row cycle reaches the same emptiness with no self-reference at
// all: rel-founding is already superseded BY rel-second, and re-minting
// it as rel-second's successor closes the ring with neither row current.
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

// The stored role is the one the projection answers with, and the upsert
// never updates it: an operator payload over a peer row passes every
// payload gate, supersedes the operator, and stays a peer.
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

// Mode-independent: the gate lives in the materializer both paths share,
// so a hand-crafted ledger cannot walk the orphaning event in through
// replay.
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

// The live door: refusal must precede signing, and it must not refuse
// the real thing — a succession naming a DIFFERENT current predecessor
// still passes the same gate.
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
