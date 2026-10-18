package store

import (
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
)

func newSupersedeTestStore(t *testing.T) *Store {
	t.Helper()
	s := testStore(t)
	seqEvent(t, s, 97, ledger.EventBeliefUpsert, map[string]any{
		"id": "b_old", "statement": "old", "ring": 3, "confidence": 0.1,
	})
	seqEvent(t, s, 98, ledger.EventBeliefUpsert, map[string]any{
		"id": "b_new", "statement": "new", "ring": 3, "confidence": 0.9,
	})
	return s
}

func countLedgerRows(t *testing.T, s *Store) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM ledger`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestBeliefSupersedeAdmissionExistingBoth: the happy path keeps working —
// a real supersede still lands exactly as the table test pins.
func TestBeliefSupersedeAdmissionExistingBoth(t *testing.T) {
	s := newSupersedeTestStore(t)
	before := countLedgerRows(t, s)

	evt := &ledger.Event{
		Seq: 99, Prev: "0", Timestamp: "2026-08-16T00:00:00Z",
		Type: ledger.EventBeliefSupersede, Ring: 3,
		Payload: mustPayload(t, map[string]any{
			"old_id": "b_old", "new_id": "b_new", "reason": "sharper",
		}),
		Content: hashOfSeq(99), Sig: "probe",
	}
	if err := s.Materialize(evt); err != nil {
		t.Fatalf("legitimate supersede must materialize: %v", err)
	}

	_, sup := getBeliefRow(t, s, "b_old")
	if !sup.Valid || sup.String != "b_new" {
		t.Fatalf("supersede must point old → new, got %v", sup)
	}
	if n := countLedgerRows(t, s); n != before+1 {
		t.Fatalf("ledger mirror must hold exactly one more row, got %d → %d", before, n)
	}
}

// TestBeliefSupersedeAdmissionArchivedSuccessor: an archived belief may
// not serve as successor — superseding into a soft-deleted row would
// resurrect a retired claim under an active pointer. Refused plainly,
// before any durability, store unchanged.
func TestBeliefSupersedeAdmissionArchivedSuccessor(t *testing.T) {
	s := newSupersedeTestStore(t)
	seqEvent(t, s, 99, ledger.EventBeliefArchive, map[string]any{
		"id": "b_new", "reason": "retired",
	})
	before := countLedgerRows(t, s)

	// Through the live door — preflight refuses before any durability.
	err := s.ValidateEvent(ledger.EventBeliefSupersede, 3,
		mustPayload(t, map[string]any{
			"old_id": "b_old", "new_id": "b_new", "reason": "resurrecting a retired belief",
		}))
	if err == nil {
		t.Fatal("supersede into an archived successor must be refused")
	}
	msg := err.Error()
	if strings.Contains(msg, "FOREIGN KEY") || strings.Contains(msg, "constraint failed") {
		t.Fatalf("refusal must be plain prose, got constraint error: %s", msg)
	}
	if !strings.Contains(msg, "b_new") {
		t.Fatalf("refusal must name the archived successor: %s", msg)
	}
	if n := countLedgerRows(t, s); n != before {
		t.Fatalf("ledger mirror must be unchanged, %d → %d", before, n)
	}
	_, sup := getBeliefRow(t, s, "b_old")
	if sup.Valid {
		t.Fatalf("b_old must remain unsuperseded, got %v", sup)
	}
}
