package store

import (
	"strings"
	"testing"
	"time"
)

// .
// .
// .
// .
func TestMeasureDerivesFromRealEvents(t *testing.T) {
	s := testStore(t)
	now := time.Now().UTC().UnixMilli()
	// .
	for i, m := range []TurnMetric{
		{TsMs: now - 1, Calls: 2, ReadOnly: 2},
		{TsMs: now - 2, Calls: 1, ReadOnly: 1},
		{TsMs: now - 3, Calls: 4, ReadOnly: 4},
		{TsMs: now - 4, Calls: 3, ReadOnly: 1},
		{TsMs: now - 5, Calls: 2, ReadOnly: 0},
	} {
		if err := s.InsertTurnMetric(m); err != nil {
			t.Fatalf("seed turn %d: %v", i, err)
		}
	}
	// .
	mustStart(t, s, "ws_v1")
	mustStart(t, s, "ws_v2")
	mustStart(t, s, "ws_w1")
	mustStart(t, s, "ws_o1")
	mustDeliver(t, s, "ws_v1", "served: checked", EvidenceLocallyVerified, "ran the check")
	mustDeliver(t, s, "ws_v2", "served: checked", EvidenceLocallyVerified, "ran the check")
	mustDeliver(t, s, "ws_w1", "served: child said so", EvidenceWorkerReportOnly, "")
	// .
	if _, err := s.SweepOrphanWorkSessions(); err != nil {
		t.Fatalf("sweep: %v", err)
	}

	r, err := s.Measure(48 * time.Hour)
	if err != nil {
		t.Fatalf("measure: %v", err)
	}
	m := byName(r)

	po := m["process_only_turns"]
	if po.Numerator != 3 || po.Denominator != 5 {
		t.Fatalf("process_only_turns = %d/%d, want 3/5 (follows turn_metrics)", po.Numerator, po.Denominator)
	}
	if len(po.Sample) == 0 {
		t.Fatal("process_only_turns must carry a reproducible sample of ts_ms")
	}

	dv := m["deliveries_reached_verification"]
	if dv.Numerator != 2 || dv.Denominator != 4 {
		t.Fatalf("deliveries_reached_verification = %d/%d, want 2/4", dv.Numerator, dv.Denominator)
	}
	amb := m["ambiguous_awaiting_inspection"]
	if amb.Numerator != 2 {
		t.Fatalf("ambiguous_awaiting_inspection numerator = %d, want 2", amb.Numerator)
	}
	iu := m["interrupted_effect_unknown"]
	if iu.Kind != "count" || iu.Numerator != 1 {
		t.Fatalf("interrupted_effect_unknown = %+v, want count 1 (the swept ws_o1)", iu)
	}

	// .
	for _, name := range []string{"context_reuse_changed_a_decision", "sdk_qual_left_host_incomplete"} {
		u := m[name]
		if u.Instrumented || u.Value() != "unknown" || u.Note == "" {
			t.Fatalf("%s must be unknown with a note, got %+v", name, u)
		}
	}
}

// .
func TestMeasureEmptyIsHonest(t *testing.T) {
	s := testStore(t)
	r, err := s.Measure(48 * time.Hour)
	if err != nil {
		t.Fatalf("measure: %v", err)
	}
	po := byName(r)["process_only_turns"]
	if po.Denominator != 0 || po.Value() != "n/a (0 of 0)" {
		t.Fatalf("empty store must read n/a, got %q", po.Value())
	}
	out := RenderMeasurement(r)
	if !strings.Contains(out, "observational") || !strings.Contains(out, "not success") {
		t.Fatalf("the readout must state it is observational and that more status is not success:\n%s", out)
	}
}

func mustStart(t *testing.T, s *Store, id string) {
	t.Helper()
	if err := s.StartWorkSession(id, "work "+id); err != nil {
		t.Fatalf("start %s: %v", id, err)
	}
}
func mustDeliver(t *testing.T, s *Store, id, result, class, readback string) {
	t.Helper()
	if err := s.DeliverWorkSession(id, result, class, readback); err != nil {
		t.Fatalf("deliver %s: %v", id, err)
	}
}
func byName(r MeasurementReadout) map[string]Metric {
	m := map[string]Metric{}
	for _, x := range r.Metrics {
		m[x.Name] = x
	}
	return m
}
