package store

import (
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
)

func TestAPortraitMayCiteANoteKeptOutOfDreamAndConsolidate(t *testing.T) {
	s := testStore(t)
	if _, err := s.db.Exec(`INSERT INTO ledger (seq, prev, ts, type, ring, payload, content) VALUES (1, 'h0', '2026-10-13T00:00:00Z', 'experience.create', 3, '{}', 'c1')`); err != nil {
		t.Fatalf("plant ledger parent: %v", err)
	}
	for _, row := range []struct{ id, category string }{
		{"exp_marked_note", "reflection"},
		{"exp_marked_experience", "observation"},
	} {
		if _, err := s.db.Exec(
			`INSERT INTO experiences (id, content, category, raw, unmetabolized, provenance, created_seq, created_at)
			 VALUES (?, 'a hard day, kept out of the passes', ?, 0, 1, 'self', 1, '2026-10-13T00:00:00Z')`, row.id, row.category); err != nil {
			t.Fatalf("plant %s: %v", row.id, err)
		}
	}
	for _, ref := range []ledger.SelfModelSourceRef{
		{Class: "notes", ID: "exp_marked_note"},
		{Class: "experiences", ID: "exp_marked_experience"},
	} {
		ok, err := s.selfModelRefExistsLocked(ref)
		if err != nil {
			t.Fatalf("the check of %s as %s: %v", ref.ID, ref.Class, err)
		}
		if !ok {
			t.Errorf("THE MARK KEEPS A NOTE OUT OF A PORTRAIT'S EVIDENCE, which is not DREAM or CONSOLIDATE: %s cited as %s is taken for a row that is not there", ref.ID, ref.Class)
		}
	}
}
