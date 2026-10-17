package store

import (
	"strings"
	"testing"
)

// A brand-new identity has no identity_lifetime row at all. That
// absence is a legitimate zero, and treating it as a failure would
// turn every fresh install into a broken one — which is the trap the
// obvious tightening of GetStats walks straight into.
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

// An error is not a zero. Every Scan in GetStats was unchecked, so a
// failing query rendered as a count of 0 and a broken store looked
// exactly like a young identity — in the one view whose job is telling
// you which of the two you are looking at.
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
	// The message has to name the query, or the operator is left
	// guessing which of eight reads failed.
	if !strings.Contains(err.Error(), "experiences") {
		t.Errorf("the error must name the failing query, got %v", err)
	}
}
