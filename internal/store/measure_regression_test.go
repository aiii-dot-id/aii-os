package store

import (
	"testing"
	"time"
)

// Completion is observed locally; it is NOT independent verification.
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

// An ordinary unknown-effect delivery is uncertainty, not a boot interruption.
func TestReviewMeasureExplicitUnknownIsNotBootInterruption(t *testing.T) {
	s := testStore(t)
	mustStart(t, s, "ws_explicit_unknown")
	mustDeliver(t, s, "ws_explicit_unknown", "partial: external response unavailable", EvidenceExternalUnknown, "")
	// No boot sweep was called; this is a normal explicit delivery.
	r, err := s.Measure(time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	m := byName(r)["interrupted_effect_unknown"]
	if m.Numerator != 0 {
		t.Fatalf("explicit delivered unknown counted as boot interruption: %s; sample=%v; want 0", m.Value(), m.Sample)
	}
}

// A sub-agent leg that had begun when the runtime stopped, delivered when
// its queue item came back rather than by the boot sweep, is the same
// interruption and is counted as one; the explicit unknown above is not.
func TestARecoveredLegThatHadBegunCountsAsAnInterruption(t *testing.T) {
	s := testStore(t)
	mustStart(t, s, "ws_leg_interrupted")
	mustDeliver(t, s, "ws_leg_interrupted", InterruptedLeg(" — leg 1 of 4 had begun"), EvidenceExternalUnknown, "")
	r, err := s.Measure(time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if m := byName(r)["interrupted_effect_unknown"]; m.Numerator != 1 || len(m.Sample) != 1 || m.Sample[0] != "ws_leg_interrupted" {
		t.Fatalf("a recovered leg's interruption must be counted: %s; sample=%v", m.Value(), m.Sample)
	}
}

// Old deliveries must not appear in a short window. The repair dates a
// delivery by delivered_at (created_seq is 0 for Ring-4 rows and cannot
// date them), so this test backdates delivered_at rather than created_seq
// — the contract (old rows excluded) is unchanged.
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
