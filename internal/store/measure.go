package store

import (
	"fmt"
	"strings"
	"time"
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
// .
// .

// .
// .
// .
// .
type Metric struct {
	Name         string
	Definition   string
	EventSource  string
	Scope        string
	Kind         string
	Instrumented bool
	Numerator    int
	Denominator  int
	Sample       []string
	Note         string
}

// .
func (m Metric) Value() string {
	if !m.Instrumented {
		return "unknown"
	}
	if m.Kind == "count" {
		return fmt.Sprintf("%d", m.Numerator)
	}
	if m.Denominator == 0 {
		return "n/a (0 of 0)"
	}
	return fmt.Sprintf("%d%% (%d of %d)", 100*m.Numerator/m.Denominator, m.Numerator, m.Denominator)
}

// .
type MeasurementReadout struct {
	WindowHours int
	Metrics     []Metric
}

// .
// .
// .
// .
// .
func (s *Store) Measure(window time.Duration) (MeasurementReadout, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := MeasurementReadout{WindowHours: int(window.Hours())}
	cutoff := time.Now().UTC().Add(-window).UnixMilli()

	// .
	// .
	// .
	var totalTurns, processOnly int
	if err := s.db.QueryRow(
		`SELECT COALESCE(SUM(CASE WHEN calls>0 THEN 1 ELSE 0 END),0), COALESCE(SUM(CASE WHEN calls>0 AND read_only=calls THEN 1 ELSE 0 END),0)
		 FROM turn_metrics WHERE ts_ms >= ?`, cutoff).Scan(&totalTurns, &processOnly); err != nil {
		return out, err
	}
	poSample, err := s.sampleStrings(`SELECT CAST(ts_ms AS TEXT) FROM turn_metrics WHERE ts_ms>=? AND calls>0 AND read_only=calls ORDER BY ts_ms DESC LIMIT 3`, cutoff)
	if err != nil {
		return out, err
	}
	out.Metrics = append(out.Metrics, Metric{
		Name:        "process_only_turns",
		Definition:  "of the tool-using turns in the window (calls>0), those whose every call was read-only — no durable or externally verified state changed. Zero-call conversational turns are excluded from both numerator and denominator, so pure conversation cannot move the ratio",
		EventSource: "turn_metrics(ts_ms, calls, read_only)", Scope: "local", Kind: "ratio", Instrumented: true,
		Numerator: processOnly, Denominator: totalTurns, Sample: poSample,
	})

	// .
	// .
	// .
	classCounts := map[string]int{}
	classified := 0
	rows, err := s.db.Query(`SELECT COALESCE(evidence,''), COUNT(*) FROM work_sessions WHERE status='delivered' AND COALESCE(evidence,'')!='' AND delivered_at >= ? GROUP BY evidence`, cutoff)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var cls string
		var n int
		if err := rows.Scan(&cls, &n); err != nil {
			rows.Close()
			return out, err
		}
		classCounts[cls] = n
		classified += n
	}
	rows.Close()

	// .
	// .
	// .
	// .
	verified := classCounts[EvidenceLocallyVerified] + classCounts[EvidenceHostReceipted]
	vSample, err := s.sampleStrings(`SELECT id||' ['||evidence||']' FROM work_sessions WHERE status='delivered' AND COALESCE(evidence,'')!='' AND delivered_at >= ? ORDER BY rowid DESC LIMIT 3`, cutoff)
	if err != nil {
		return out, err
	}
	out.Metrics = append(out.Metrics, Metric{
		Name:        "deliveries_reached_verification",
		Definition:  "delivered results in a locally-verified or host-receipted class, of all CLASSIFIED deliveries in the window (not a success rate over all attempts — an unclassified delivery, e.g. an unserved outcome with no explicit class, is excluded) — completion (completed_locally), a worker report, or an unknown effect is NOT verification",
		EventSource: "work_sessions(evidence, delivered_at)", Scope: "worker→local", Kind: "ratio", Instrumented: true,
		Numerator: verified, Denominator: classified, Sample: vSample,
	})

	// .
	// .
	ambClasses := ambiguousEvidenceClasses()
	ambiguous := 0
	inPlaceholders := make([]string, len(ambClasses))
	aArgs := make([]interface{}, 0, len(ambClasses)+1)
	for i, c := range ambClasses {
		ambiguous += classCounts[c]
		inPlaceholders[i] = "?"
		aArgs = append(aArgs, c)
	}
	aArgs = append(aArgs, cutoff)
	aSample, err := s.sampleStrings(`SELECT id||' ['||evidence||']' FROM work_sessions WHERE status='delivered' AND evidence IN (`+strings.Join(inPlaceholders, ",")+`) AND delivered_at >= ? ORDER BY rowid DESC LIMIT 3`, aArgs...)
	if err != nil {
		return out, err
	}
	out.Metrics = append(out.Metrics, Metric{
		Name:        "ambiguous_awaiting_inspection",
		Definition:  "delivered results in an ambiguous class (worker-reported, unknown-external, partial/mixed, rejected) in the window — each owes a distinct read-only recovery, none of them a blind retry; the settled not_run/completed classes and any unclassified delivery are excluded",
		EventSource: "work_sessions(evidence, delivered_at)", Scope: "worker→local", Kind: "ratio", Instrumented: true,
		Numerator: ambiguous, Denominator: classified, Sample: aSample,
	})

	// .
	// .
	// .
	var interrupted int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM work_sessions WHERE evidence='external_effect_unknown' AND result LIKE 'FAILED: interrupted by a runtime restart%' AND delivered_at >= ?`, cutoff).Scan(&interrupted); err != nil {
		return out, err
	}
	iSample, err := s.sampleStrings(`SELECT id FROM work_sessions WHERE evidence='external_effect_unknown' AND result LIKE 'FAILED: interrupted by a runtime restart%' AND delivered_at >= ? ORDER BY rowid DESC LIMIT 3`, cutoff)
	if err != nil {
		return out, err
	}
	out.Metrics = append(out.Metrics, Metric{
		Name:        "interrupted_effect_unknown",
		Definition:  "sessions interrupted before delivery by a runtime restart (the boot sweep's marker) in the window — the recovery surface points to an inspection, not a retry; an ordinary unknown-effect delivery is NOT counted here",
		EventSource: "work_sessions(evidence, result, delivered_at)", Scope: "local", Kind: "count", Instrumented: true,
		Numerator: interrupted, Sample: iSample,
	})

	// .
	out.Metrics = append(out.Metrics, Metric{
		Name:        "context_reuse_changed_a_decision",
		Definition:  "retrieved context that changed a later decision or prevented repetition",
		EventSource: "none", Scope: "unknown", Instrumented: false,
		Note: "no event links a recall to a later decision; measuring this needs new instrumentation, deferred per the plan. Reported unknown, never estimated.",
	})
	out.Metrics = append(out.Metrics, Metric{
		Name:        "sdk_qual_left_host_incomplete",
		Definition:  "SDK local qualification runs that correctly left host verification incomplete",
		EventSource: "aii-plugin-sdk: aiisdk test -report (schema aiisdk.qual.v1)", Scope: "host", Instrumented: false,
		Note: "SDK qualification runs in the plugin-sdk repo and is not ingested here; reproduce from a qual.json where signature/host-activation/host-receipt are not_run and manifest_hash-consumer is incomplete.",
	})
	return out, nil
}

func (s *Store) sampleStrings(q string, args ...interface{}) ([]string, error) {
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// .
// .
// .
func RenderMeasurement(r MeasurementReadout) string {
	var b strings.Builder
	fmt.Fprintf(&b, "### Outcome measurement — last %dh (observational; not an authority, and more status is not success)\n", r.WindowHours)
	for _, m := range r.Metrics {
		fmt.Fprintf(&b, "\n%s: %s\n  what: %s\n  source: %s · scope: %s\n", m.Name, m.Value(), m.Definition, m.EventSource, m.Scope)
		if !m.Instrumented {
			fmt.Fprintf(&b, "  unknown: %s\n", m.Note)
			continue
		}
		if len(m.Sample) > 0 {
			fmt.Fprintf(&b, "  reproduce from: %s\n", strings.Join(m.Sample, ", "))
		}
	}
	return b.String()
}
