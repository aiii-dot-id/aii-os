package store

import (
	"path/filepath"
	"testing"
	"time"
)

func TestPlanCalibrationMeasuresTheRemainingRequestWindow(t *testing.T) {
	for _, tt := range []struct {
		name                     string
		row                      TurnMetric
		plans, predicted, actual int
	}{
		{"late remaining estimate", TurnMetric{Calls: 80, Predicted: 19, DeclaredOrdinal: 61, Emissions: &ToolCallEmissions{Count: 80, Complete: true}}, 1, 19, 19},
		{"earlier refused requests", TurnMetric{Calls: 4, Predicted: 2, DeclaredOrdinal: 6, Emissions: &ToolCallEmissions{Count: 8, Complete: true}}, 1, 2, 2},
		{"later refused requests", TurnMetric{Calls: 2, Predicted: 3, DeclaredOrdinal: 2, Emissions: &ToolCallEmissions{Count: 5, Complete: true}}, 1, 3, 3},
		{"empty measured window", TurnMetric{Calls: 6, Predicted: 1, DeclaredOrdinal: 6, Emissions: &ToolCallEmissions{Count: 6, Complete: true}}, 1, 1, 0},
		{"legacy count unknown", TurnMetric{Calls: 10, Predicted: 3, DeclaredOrdinal: 2}, 0, 0, 0},
		{"incomplete count", TurnMetric{Calls: 10, Predicted: 3, DeclaredOrdinal: 2, Emissions: &ToolCallEmissions{Count: 10}}, 0, 0, 0},
		{"declaration position unknown", TurnMetric{Calls: 10, Predicted: 3, Emissions: &ToolCallEmissions{Count: 10, Complete: true}}, 0, 0, 0},
		{"declaration after final position", TurnMetric{Calls: 10, Predicted: 3, DeclaredOrdinal: 11, Emissions: &ToolCallEmissions{Count: 10, Complete: true}}, 0, 0, 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s, err := New(filepath.Join(t.TempDir(), "aii.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			tt.row.TsMs = time.Now().UTC().UnixMilli()
			if err := s.InsertTurnMetric(tt.row); err != nil {
				t.Fatal(err)
			}
			got, err := s.PlanCalibration(48 * time.Hour)
			if err != nil {
				t.Fatal(err)
			}
			want := PlanCalibrationStats{Plans: tt.plans, Predicted: tt.predicted, Actual: tt.actual, Unmeasured: 1 - tt.plans}
			if got != want {
				t.Fatalf("calibration = %+v, want %+v", got, want)
			}
		})
	}
}

func TestPlanCalibrationHistoricalReadOnlyWindowsStayUnknown(t *testing.T) {
	for _, missing := range []string{"emitted_calls", "emission_coverage_complete"} {
		t.Run(missing, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "aii.db")
			s, err := New(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := s.InsertTurnMetric(TurnMetric{TsMs: time.Now().UTC().UnixMilli(), Calls: 10, Predicted: 3, DeclaredOrdinal: 2,
				Emissions: &ToolCallEmissions{Count: 10, Complete: true}}); err != nil {
				t.Fatal(err)
			}
			if _, err := s.DB().Exec(`ALTER TABLE turn_metrics DROP COLUMN ` + missing); err != nil {
				t.Fatal(err)
			}
			history := olderColumns(t, s)
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			s, err = OpenReadOnly(path)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			got, err := s.PlanCalibration(48 * time.Hour)
			if err != nil || got != (PlanCalibrationStats{Unmeasured: 1}) {
				t.Fatalf("historical calibration = %+v, err=%v", got, err)
			}
			if got := olderColumns(t, s); got != history {
				t.Fatalf("reading calibration changed historical rows: %s -> %s", history, got)
			}
			var columns int
			if err := s.DB().QueryRow(`SELECT COUNT(*) FROM pragma_table_info('turn_metrics') WHERE name=?`, missing).Scan(&columns); err != nil || columns != 0 {
				t.Fatalf("historical schema changed: restored columns=%d err=%v", columns, err)
			}
		})
	}
}
