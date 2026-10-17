package store

import (
	"path/filepath"
	"testing"
)

func TestToolEmissionMetricsPreserveUnknownAndIncompleteAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "aii.db")
	s, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	for i, m := range []*ToolCallEmissions{nil, {Count: 0, Complete: true}, {Count: 5, Complete: false}, {Count: 7, Complete: true}} {
		if err := s.InsertTurnMetric(TurnMetric{TsMs: int64(i + 1), Calls: 2, Emissions: m}); err != nil {
			t.Fatal(err)
		}
		if err := s.InsertSubagentMetric(SubagentMetric{SessionID: string(rune('a' + i)), TsMs: int64(i + 1), Calls: 2, Emissions: m}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = New(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, table := range []string{"turn_metrics", "subagent_metrics"} {
		rows, err := s.DB().Query(`SELECT calls, emitted_calls, emission_coverage_complete FROM ` + table + ` ORDER BY ts_ms`)
		if err != nil {
			t.Fatal(err)
		}
		i := 0
		for rows.Next() {
			var calls int
			var count, complete *int
			if err := rows.Scan(&calls, &count, &complete); err != nil {
				t.Fatal(err)
			}
			if calls != 2 {
				t.Fatal("emission measurement changed the existing meter")
			}
			if i == 0 {
				if count != nil || complete != nil {
					t.Fatal("unmeasured became known", count, complete)
				}
			} else {
				wantCount, wantComplete := []int{0, 0, 5, 7}[i], []int{0, 1, 0, 1}[i]
				if count == nil || complete == nil || *count != wantCount || *complete != wantComplete {
					t.Fatalf("%s row %d changed its measurement", table, i)
				}
			}
			i++
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
		if i != 4 {
			t.Fatalf("%s lost rows: %d", table, i)
		}
	}
	if err := olderReconcile(t, s); err == nil {
		t.Fatal("an older schema discarded measured emissions")
	}
}
