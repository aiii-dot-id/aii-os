package app

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/store"
)

// .
// .
// .
// .
func TestReviewMeasureInspectionMustNotImproveProcessMetric(t *testing.T) {
	for _, extraInspection := range []bool{false, true} {
		name := "read_only"
		if extraInspection {
			name = "read_plus_measure"
		}
		t.Run(name, func(t *testing.T) {
			st, err := store.New(filepath.Join(t.TempDir(), "review.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { st.Close() })
			a := &App{store: st}
			a.countToolCall("read", `{"file_path":"/tmp/fixture"}`)
			if extraInspection {
				// .
				a.countToolCall("work", `{"action":"measure","hours":1}`)
				if _, err := st.Measure(time.Hour); err != nil {
					t.Fatal(err)
				}
			}
			a.logTurnSummary()
			r, err := st.Measure(time.Hour)
			if err != nil {
				t.Fatal(err)
			}
			for _, m := range r.Metrics {
				if m.Name == "process_only_turns" && (m.Numerator != 1 || m.Denominator != 1) {
					t.Fatalf("adding a read-only measure call changed process-only classification: %s; want 100%% (1 of 1)", m.Value())
				}
			}
		})
	}
}
