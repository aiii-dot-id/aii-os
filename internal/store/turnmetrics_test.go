package store

import (
	"path/filepath"
	"testing"
	"time"
)

// .
// .
// .
func TestTurnMetricRoundTrip(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), "aii.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if err := s.InsertTurnMetric(TurnMetric{TsMs: time.Now().UnixMilli() - time.Hour.Milliseconds(), Calls: 10, ReadOnly: 7, Spawned: 1, Harvested: 2}); err != nil {
		t.Fatal(err)
	}
	if err := s.InsertTurnMetric(TurnMetric{TsMs: time.Now().UnixMilli(), Calls: 5, ReadOnly: 5, Spawned: 0, Harvested: 0}); err != nil {
		t.Fatal(err)
	}
	turns, calls, roPct, spawns, harvests, _, err := s.RhythmStats(48 * time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if turns != 2 || calls != 15 || roPct != 80 || spawns != 1 || harvests != 2 {
		t.Fatalf("rhythm stats wrong: turns=%d calls=%d ro%%=%d spawns=%d harvests=%d", turns, calls, roPct, spawns, harvests)
	}
}
