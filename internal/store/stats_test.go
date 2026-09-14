package store

import (
	"strings"
	"testing"
)

// .
// .
// .
// .
func TestGetStatsOnAFreshStoreIsZeroNotAnError(t *testing.T) {
	s := testStore(t)

	stats, err := s.GetStats()
	if err != nil {
		t.Fatalf("a fresh store is empty, not broken: %v", err)
	}
	if stats.BeliefCount != 0 || stats.LifetimeTicks != 0 {
		t.Errorf("fresh store should report zeros, got %+v", stats)
	}
}

// .
// .
// .
// .
func TestGetStatsReportsAFailingQueryInsteadOfZero(t *testing.T) {
	s := testStore(t)

	if _, err := s.DB().Exec(`DROP TABLE experiences`); err != nil {
		t.Fatal(err)
	}

	stats, err := s.GetStats()
	if err == nil {
		t.Fatalf("a missing table must be reported, not counted as 0 (got %+v)", stats)
	}
	if stats != nil {
		t.Errorf("a failed stats read must not also hand back a half-filled answer: %+v", stats)
	}
	// .
	// .
	if !strings.Contains(err.Error(), "experiences") {
		t.Errorf("the error must name the failing query, got %v", err)
	}
}
