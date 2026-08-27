package store

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
)

// TensionsView: derived contradiction surface — present while the
// CONTRADICTS edge lives, vanished on archive. Zero lifecycle.
func TestTensionsViewDerived(t *testing.T) {
	s := testStore(t)
	seq := 0
	mat := func(et ledger.EventType, payload map[string]interface{}) {
		seq++
		b, _ := json.Marshal(payload)
		if err := s.Materialize(&ledger.Event{Seq: uint64(seq), Type: et, Payload: b,
			Timestamp: time.Now().UTC().Format(time.RFC3339Nano)}); err != nil {
			t.Fatalf("materialize %s: %v", et, err)
		}
	}
	mat(ledger.EventBeliefUpsert, map[string]interface{}{"id": "b1", "statement": "I work best alone", "ring": 3, "confidence": 0.5})
	mat(ledger.EventBeliefUpsert, map[string]interface{}{"id": "b2", "statement": "collaboration multiplies me", "ring": 3, "confidence": 0.5})
	mat(ledger.EventEdgeCreate, map[string]interface{}{"id": "t1", "from_id": "b1", "to_id": "b2", "edge_type": "CONTRADICTS"})

	pairs, err := s.TensionsView()
	if err != nil || len(pairs) != 1 {
		t.Fatalf("one standing tension, got %d %v", len(pairs), err)
	}
	stmts, _ := s.StatementsFor([]string{"b1", "b2"})
	if stmts["b1"] != "I work best alone" || stmts["b2"] != "collaboration multiplies me" {
		t.Fatalf("statements resolve: %v", stmts)
	}

	// Resolution vanishes it — the whole lifecycle, in one archive.
	mat(ledger.EventEdgeArchive, map[string]interface{}{"id": "t1"})
	pairs2, _ := s.TensionsView()
	if len(pairs2) != 0 {
		t.Fatalf("archived contradiction must vanish from the view, got %d", len(pairs2))
	}
}
