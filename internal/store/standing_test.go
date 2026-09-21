package store

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
)

// .
// .
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
	ends, err := s.TensionEnds([]string{"b1", "b2"})
	if err != nil || ends["b1"].Text != "I work best alone" || ends["b2"].Text != "collaboration multiplies me" ||
		ends["b1"].Kind != "belief" || ends["b1"].Retired {
		t.Fatalf("both ends resolve as standing beliefs: %+v %v", ends, err)
	}

	// .
	mat(ledger.EventEdgeArchive, map[string]interface{}{"id": "t1"})
	pairs2, _ := s.TensionsView()
	if len(pairs2) != 0 {
		t.Fatalf("archived contradiction must vanish from the view, got %d", len(pairs2))
	}
}

// .
// .
// .
// .
// .
func TestTensionEndsSayWhatEachEndIs(t *testing.T) {
	s := testStore(t)
	seq := 0
	mat := func(et ledger.EventType, payload map[string]interface{}) {
		t.Helper()
		seq++
		b, _ := json.Marshal(payload)
		if err := s.Materialize(&ledger.Event{Seq: uint64(seq), Type: et, Payload: b,
			Timestamp: time.Now().UTC().Format(time.RFC3339Nano)}); err != nil {
			t.Fatalf("materialize %s: %v", et, err)
		}
	}
	const secret = "THE SEALED WORDS nobody but the identity may read"
	mat(ledger.EventBeliefUpsert, map[string]interface{}{"id": "b_old", "statement": "I work best alone", "ring": 3, "confidence": 0.5})
	mat(ledger.EventBeliefUpsert, map[string]interface{}{"id": "b_new", "statement": "I work best in pairs", "ring": 3, "confidence": 0.5})
	mat(ledger.EventBeliefSupersede, map[string]interface{}{"old_id": "b_old", "new_id": "b_new", "reason": "changed"})
	mat(ledger.EventExperienceCreate, map[string]interface{}{"id": "exp_open", "content": "a colleague wrote that pairing slows them down", "category": "observation", "provenance": "external", "source_url": "https://example.org/pairing"})
	mat(ledger.EventExperienceCreate, map[string]interface{}{"id": "exp_sealed", "content": secret, "category": "reflection", "provenance": "self", "private": true})

	ends, err := s.TensionEnds([]string{"b_old", "b_new", "exp_open", "exp_sealed", "gone", "b_new"})
	if err != nil {
		t.Fatal(err)
	}
	if e := ends["b_old"]; e.Kind != "belief" || !e.Retired || e.Text != "I work best alone" {
		t.Fatalf("a superseded belief must resolve as retired: %+v", e)
	}
	if e := ends["b_new"]; e.Kind != "belief" || e.Retired {
		t.Fatalf("a standing belief: %+v", e)
	}
	if e := ends["exp_open"]; e.Kind != "experience" || e.Sealed || e.Provenance != "external" || e.Text == "" {
		t.Fatalf("an open note resolves with its words and whose they are: %+v", e)
	}
	if e := ends["exp_sealed"]; e.Kind != "experience" || !e.Sealed || e.Text != "" {
		t.Fatalf("A PRIVATE NOTE'S WORDS LEFT THE STORE: %+v", e)
	}
	if e := ends["gone"]; e.Kind != "" || e.ID != "gone" {
		t.Fatalf("an id that names nothing resolves to nothing, by name: %+v", e)
	}
}

// .
// .
// .
// .
// .
func TestTensionEndsRefuseAnUnreadableStore(t *testing.T) {
	t.Run("nothing readable", func(t *testing.T) {
		s := testStore(t)
		if err := s.db.Close(); err != nil {
			t.Fatal(err)
		}
		if ends, err := s.TensionEnds([]string{"b1"}); err == nil {
			t.Fatalf("an unreadable store resolved ends: %+v", ends)
		}
	})
	t.Run("only the beliefs unreadable", func(t *testing.T) {
		s := testStore(t)
		if _, err := s.db.Exec(`PRAGMA foreign_keys = OFF; ALTER TABLE beliefs RENAME TO beliefs_unreadable`); err != nil {
			t.Fatalf("fixture: %v", err)
		}
		ends, err := s.TensionEnds([]string{"b1"})
		if err == nil {
			t.Fatalf("UNREADABLE BELIEFS WERE CALLED ABSENT: b1 resolved as %+v", ends["b1"])
		}
	})
	t.Run("only the experiences unreadable", func(t *testing.T) {
		s := testStore(t)
		if _, err := s.db.Exec(`PRAGMA foreign_keys = OFF; ALTER TABLE experiences RENAME TO experiences_unreadable`); err != nil {
			t.Fatalf("fixture: %v", err)
		}
		if ends, err := s.TensionEnds([]string{"exp_1"}); err == nil {
			t.Fatalf("UNREADABLE EXPERIENCES WERE CALLED ABSENT: %+v", ends["exp_1"])
		}
	})
}
