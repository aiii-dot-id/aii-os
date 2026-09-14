package store

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
)

// .
// .
// .
func probeEvent(seq uint64, typ ledger.EventType, ring int, payload map[string]interface{}) ledger.Event {
	raw, _ := json.Marshal(payload)
	return ledger.Event{
		Seq: seq, Prev: fmt.Sprintf("prev-%d", seq),
		Timestamp: "2026-08-20T00:00:00Z", Type: typ,
		Ring: ring, Payload: raw, Content: fmt.Sprintf("hash-%d", seq),
		Sig: "sig",
	}
}

// .
// .
// .
// .
// .
func TestReplayFailurePreservesPriorProjection(t *testing.T) {
	s := testStore(t)

	// .
	prior := []ledger.Event{
		probeEvent(1, ledger.EventBeliefUpsert, 3, map[string]interface{}{
			"id": "b1", "statement": "prior admitted truth", "ring": 3, "confidence": 0.9,
		}),
	}
	if err := s.ReplayAll(EventSlice(prior)); err != nil {
		t.Fatalf("prior replay: %v", err)
	}

	// .
	// .
	// .
	poisoned := []ledger.Event{
		probeEvent(1, ledger.EventBeliefPromote, 3, map[string]interface{}{
			"id": "ghost", "ring": 3,
		}),
	}
	if err := s.ReplayAll(EventSlice(poisoned)); err == nil {
		t.Fatal("poisoned replay must fail")
	}

	var beliefs, mirror int
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM beliefs WHERE id = 'b1'`).Scan(&beliefs); err != nil {
		t.Fatal(err)
	}
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM ledger`).Scan(&mirror); err != nil {
		t.Fatal(err)
	}
	if beliefs != 1 {
		t.Fatalf("failed rebuild destroyed the prior projection: belief b1 gone (count %d)", beliefs)
	}
	if mirror != 1 {
		t.Fatalf("failed rebuild destroyed the ledger mirror: %d rows (want the prior 1)", mirror)
	}
}

// .
// .
// .
// .
// .
func TestLiveMaterializeMirrorAndEffectAreOneTransaction(t *testing.T) {
	s := testStore(t)

	evt := probeEvent(7, ledger.EventBeliefPromote, 3, map[string]interface{}{
		"id": "ghost", "ring": 3,
	})
	if err := s.Materialize(&evt); err == nil {
		t.Fatal("promote of an unknown belief must fail")
	}

	var mirror int
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM ledger WHERE seq = 7`).Scan(&mirror); err != nil {
		t.Fatal(err)
	}
	if mirror != 0 {
		t.Fatalf("mirror row committed without its effect — mirror+effect must be one transaction (rows %d)", mirror)
	}
}

func TestLiveMaterializeCannotReplaceMirrorSequence(t *testing.T) {
	s := testStore(t)
	original := probeEvent(1, ledger.EventBeliefUpsert, 3, map[string]interface{}{
		"id": "original", "statement": "original truth", "ring": 3, "confidence": 1,
	})
	if err := s.Materialize(&original); err != nil {
		t.Fatal(err)
	}
	replacement := probeEvent(1, ledger.EventBeliefUpsert, 3, map[string]interface{}{
		"id": "replacement", "statement": "replacement truth", "ring": 3, "confidence": 1,
	})
	replacement.Content = "replacement-hash"
	if err := s.Materialize(&replacement); err == nil {
		t.Fatal("duplicate ledger sequence replaced the admitted mirror row")
	}

	var contentHash string
	if err := s.DB().QueryRow(`SELECT content FROM ledger WHERE seq = 1`).Scan(&contentHash); err != nil {
		t.Fatal(err)
	}
	if contentHash != original.Content {
		t.Fatalf("mirror content hash = %q, want original %q", contentHash, original.Content)
	}
	var replacements int
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM beliefs WHERE id = 'replacement'`).Scan(&replacements); err != nil {
		t.Fatal(err)
	}
	if replacements != 0 {
		t.Fatal("replacement projection effect escaped the refused transaction")
	}
}
