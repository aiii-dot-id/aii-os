package store

import (
	"strings"
	"testing"
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

func TestADuplicateLookupFailureIsNotAMiss(t *testing.T) {
	s := testStore(t)

	// .
	if id, err := s.FindExperienceByContent("nothing has ever said this"); err != nil || id != "" {
		t.Fatalf("clean miss returned (%q, %v), want (\"\", nil)", id, err)
	}
	if id, err := s.FindBeliefByStatement("nor this", ""); err != nil || id != "" {
		t.Fatalf("clean miss returned (%q, %v), want (\"\", nil)", id, err)
	}

	// .
	if err := s.db.Close(); err != nil {
		t.Fatal(err)
	}
	id, err := s.FindExperienceByContent("anything at all")
	if err == nil {
		t.Errorf("a failed experience lookup returned (%q, nil) — indistinguishable from no duplicate", id)
	}
	if id != "" {
		t.Errorf("a failed lookup returned an id: %q", id)
	}
	if err != nil && !strings.Contains(err.Error(), "duplicate experience") {
		t.Errorf("the error does not say what it was doing: %v", err)
	}
	if _, err := s.FindBeliefByStatement("anything", ""); err == nil {
		t.Error("a failed belief lookup returned no error — the same defect on the belief side")
	}
}

// .
// .
// .
func TestARealDuplicateIsStillFound(t *testing.T) {
	s := testStore(t)
	if _, err := s.db.Exec(`INSERT INTO ledger
		(seq, prev, ts, type, payload, content, sig)
		VALUES (1,'','2026-01-01T00:00:00Z','experience.create','{}','h','sig')`); err != nil {
		t.Fatalf("seed ledger: %v", err)
	}
	if _, err := s.db.Exec(`INSERT INTO experiences (id, content, created_seq, created_at)
		VALUES (?,?,?,?)`, "exp_dupe", "the same thing twice", 1, "2026-01-01T00:00:00Z"); err != nil {
		t.Fatalf("seed experience: %v", err)
	}
	got, err := s.FindExperienceByContent("the same thing twice")
	if err != nil || got != "exp_dupe" {
		t.Fatalf("duplicate lookup returned (%q, %v), want (\"exp_dupe\", nil)", got, err)
	}
}
