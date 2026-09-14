package store

import (
	"strings"
	"testing"
	"time"
)

// .
// .
func TestReview2ProcessOnlyExcludesZeroCallTurns(t *testing.T) {
	s := testStore(t)
	now := time.Now().UTC().UnixMilli()
	seed := []TurnMetric{
		{TsMs: now - 1, Calls: 1, ReadOnly: 1},
		{TsMs: now - 2, Calls: 2, ReadOnly: 2},
		{TsMs: now - 3, Calls: 3, ReadOnly: 1},
		{TsMs: now - 4, Calls: 2, ReadOnly: 0},
		{TsMs: now - 5, Calls: 0, ReadOnly: 0},
		{TsMs: now - 6, Calls: 0, ReadOnly: 0},
		{TsMs: now - 7, Calls: 0, ReadOnly: 0},
	}
	for i, m := range seed {
		if err := s.InsertTurnMetric(m); err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
	}
	po := byName(mustMeasure(t, s))["process_only_turns"]
	if po.Numerator != 2 || po.Denominator != 4 {
		t.Fatalf("process_only = %d/%d, want 2/4 (denominator = tool-using turns only, not 7)", po.Numerator, po.Denominator)
	}
}

// .
// .
// .
func TestReview2AmbiguousCountsOnlyRecoverableClasses(t *testing.T) {
	s := testStore(t)
	mustStart(t, s, "ws_notrun")
	mustDeliver(t, s, "ws_notrun", "unserved: never attempted", EvidenceNotRun, "")
	mustStart(t, s, "ws_rej")
	mustDeliver(t, s, "ws_rej", "unserved: refused at the contract", EvidenceRejectedNoEffect, "")

	m := byName(mustMeasure(t, s))["ambiguous_awaiting_inspection"]
	if m.Numerator != 1 {
		t.Fatalf("ambiguous numerator = %d, want 1 (rejected only; settled not_run excluded)", m.Numerator)
	}
	var sawRejected bool
	for _, s := range m.Sample {
		if strings.Contains(s, "rejected_before_effect") {
			sawRejected = true
		}
		if strings.Contains(s, "not_run") {
			t.Fatalf("settled not_run must not appear in the ambiguous sample: %v", m.Sample)
		}
	}
	if !sawRejected {
		t.Fatalf("a counted rejected delivery must be reproducible in the sample: %v", m.Sample)
	}
}

func mustMeasure(t *testing.T, s *Store) MeasurementReadout {
	t.Helper()
	r, err := s.Measure(time.Hour)
	if err != nil {
		t.Fatalf("measure: %v", err)
	}
	return r
}
