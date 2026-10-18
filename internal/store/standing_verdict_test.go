package store

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
)

// verdictGraph is n beliefs, each with three incoming supports
// (confirmed) but every third, which has two (new); every fifth is also
// contradicted (suspect); and every belief points out at an experience
// of its own.
func verdictGraph(tb testing.TB, n int) *Store {
	tb.Helper()
	s := testStore(tb)
	var seq uint64
	mat := func(kind ledger.EventType, ring int, payload map[string]any) {
		tb.Helper()
		seq++
		body, err := json.Marshal(payload)
		if err != nil {
			tb.Fatal(err)
		}
		if err := s.Materialize(&ledger.Event{Seq: seq, Type: kind, Ring: ring, Timestamp: "2026-09-23T00:00:00Z", Payload: body}); err != nil {
			tb.Fatalf("materialize %s: %v", kind, err)
		}
	}
	mat(ledger.EventRing0Genesis, 0, map[string]any{"name": "S"})
	edge := func(id, from, to, kind string) {
		mat(ledger.EventEdgeCreate, 3, map[string]any{"id": id, "from_id": from, "to_id": to, "edge_type": kind})
	}
	for i := 0; i < n; i++ {
		b := fmt.Sprintf("b%03d", i)
		mat(ledger.EventBeliefUpsert, 3, map[string]any{"id": b, "statement": "claim " + b, "ring": 3, "confidence": 0.5})
		for j := 0; j < 3; j++ {
			if j == 2 && i%3 == 0 {
				continue // two sources: new
			}
			x := fmt.Sprintf("x%03d_%d", i, j)
			mat(ledger.EventExperienceCreate, 3, map[string]any{"id": x, "content": x, "provenance": "self"})
			edge("s_"+x, x, b, "SUPPORTS")
		}
		if i%5 == 0 {
			edge("c_"+b, fmt.Sprintf("x%03d_0", i), b, "CONTRADICTS")
		}
		edge("out_"+b, b, fmt.Sprintf("x%03d_1", i), "SUPPORTS")
	}
	return s
}

func TestTheVerdictReadsOnlyTheEdgesIntoTheBelief(t *testing.T) {
	s := verdictGraph(t, 30)
	verdicts := map[string]int{}
	for i := 0; i < 30; i++ {
		id := fmt.Sprintf("b%03d", i)
		got, err := s.StandingFor(id)
		if err != nil {
			t.Fatal(err)
		}
		report, err := s.StandingReportFor(id)
		if err != nil {
			t.Fatal(err)
		}
		if got != report.Standing {
			t.Errorf("%s: StandingFor says %s, the full report %s", id, got, report.Standing)
		}
		if report.ExcludedTotal == 0 {
			t.Errorf("%s: the report counts no excluded edge; its outgoing edge was not read", id)
		}
		verdicts[got]++
	}
	for _, want := range []string{"confirmed", "suspect", "new"} {
		if verdicts[want] == 0 {
			t.Fatalf("no %s belief in the fixture (%v): it decides nothing", want, verdicts)
		}
	}

	plan := func(query string, args ...any) string {
		rows, err := s.db.Query("EXPLAIN QUERY PLAN "+query, args...)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var out []string
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
				t.Fatal(err)
			}
			out = append(out, detail)
		}
		return strings.Join(out, "\n")
	}
	if p := plan(standingIncomingEdgesSQL, "b000"); !strings.Contains(p, "SEARCH edges USING INDEX idx_edges_to (to_id=?)") {
		t.Errorf("the verdict's edge read is not a search of idx_edges_to:\n%s", p)
	}
	if p := plan(standingEdgesForSQL, "b000", "b000"); strings.Contains(p, "SEARCH edges USING INDEX idx_edges_to (to_id=?)") {
		t.Errorf("the control no longer walks the index; the comparison means nothing:\n%s", p)
	}
}

// BenchmarkStandingVerdict is one belief's verdict in a graph of 200,
// through the edges into it and through both directions.
func BenchmarkStandingVerdict(b *testing.B) {
	s := verdictGraph(b, 200)
	for name, every := range map[string]bool{"incoming": false, "both-directions": true} {
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				s.mu.RLock()
				_, err := s.standingReportRead(s.db, fmt.Sprintf("b%03d", i%200), nil, every)
				s.mu.RUnlock()
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
