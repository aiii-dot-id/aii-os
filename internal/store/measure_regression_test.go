package store

import (
	"testing"
	"time"
)

// .
// .
// .

// .
func TestReviewMeasureUnverifiedCompletionIsNotVerification(t *testing.T) {
	s := testStore(t)
	mustStart(t, s, "ws_unchecked")
	mustDeliver(t, s, "ws_unchecked", "served: completed, no readback", EvidenceCompletedLocally, "")
	r, err := s.Measure(time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	m := byName(r)["deliveries_reached_verification"]
	if m.Numerator != 0 || m.Denominator != 1 {
		t.Fatalf("unchecked completed_locally counted as verification: %s; want 0%% (0 of 1)", m.Value())
	}
}

// .
func TestReviewMeasureExplicitUnknownIsNotBootInterruption(t *testing.T) {
	s := testStore(t)
	mustStart(t, s, "ws_explicit_unknown")
	mustDeliver(t, s, "ws_explicit_unknown", "partial: external response unavailable", EvidenceExternalUnknown, "")
	// .
	r, err := s.Measure(time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	m := byName(r)["interrupted_effect_unknown"]
	if m.Numerator != 0 {
		t.Fatalf("explicit delivered unknown counted as boot interruption: %s; sample=%v; want 0", m.Value(), m.Sample)
	}
}

// .
// .
// .
// .
func TestReviewMeasureOldDeliveriesOutsideWindow(t *testing.T) {
	s := testStore(t)
	oldMs := time.Now().UTC().Add(-72 * time.Hour).UnixMilli()
	for _, x := range []struct{ id, class string }{
		{"ws_old_verified", EvidenceLocallyVerified},
		{"ws_old_worker", EvidenceWorkerReportOnly},
		{"ws_old_unknown", EvidenceExternalUnknown},
	} {
		mustStart(t, s, x.id)
		rb := ""
		if EvidenceVerifiedTier(x.class) {
			rb = "fixture readback"
		}
		mustDeliver(t, s, x.id, "served: historical fixture", x.class, rb)
		if _, err := s.db.Exec(`UPDATE work_sessions SET delivered_at=? WHERE id=?`, oldMs, x.id); err != nil {
			t.Fatal(err)
		}
	}
	r, err := s.Measure(time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	m := byName(r)
	for _, name := range []string{"deliveries_reached_verification", "ambiguous_awaiting_inspection", "interrupted_effect_unknown"} {
		x := m[name]
		if x.Numerator != 0 || x.Denominator != 0 || len(x.Sample) != 0 {
			t.Errorf("%s includes 72h-old rows in last 1h: %s; sample=%v", name, x.Value(), x.Sample)
		}
	}
}
