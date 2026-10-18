package store

import (
	"path/filepath"
	"testing"
	"time"
)

// The reader behind the recurring set's line: what the
// turns timers opened have said about their own size, each wake counted once
// in the class its row puts it in.

func wakeStore(t *testing.T) *Store {
	t.Helper()
	s, err := New(filepath.Join(t.TempDir(), "aii.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// turnAt records a turn that ended ago before now, under source.
func turnAt(t *testing.T, s *Store, ago time.Duration, source string, rounds int, u *TurnUsage) {
	t.Helper()
	if err := s.InsertTurnMetric(TurnMetric{TsMs: time.Now().Add(-ago).UnixMilli(), Rounds: rounds, Source: source, Usage: u}); err != nil {
		t.Fatal(err)
	}
}

func tokens(total, silent, unknown int) *TurnUsage {
	return &TurnUsage{PromptTokens: total, TotalTokens: total, Silent: silent, UnknownAttempts: unknown}
}

// Complete is the loop's rule — a round, no silent call, no attempt of
// unknown usage — and only those wakes are sizes. A floor keeps its lower
// bound out of the range; nothing that told nothing is a floor of zero, and
// nothing unknown is zero.
func TestTimerWakeUsageClassifiesEveryWake(t *testing.T) {
	s := wakeStore(t)
	for i, w := range []struct {
		rounds int
		usage  *TurnUsage
	}{
		{1, tokens(100, 0, 0)},
		{3, tokens(300, 0, 0)},
		{2, tokens(200, 0, 0)},
		{1, tokens(0, 0, 0)},   // a call that reported zero is a measurement of zero
		{2, tokens(500, 1, 0)}, // a silent call: a floor
		{2, tokens(900, 0, 1)}, // an attempt of unknown usage: a floor
		{0, tokens(5, 0, 0)},   // reported with no round behind it: not exact
		{2, tokens(0, 2, 0)},   // no call reported: unknown, not a floor of zero
		{2, nil},               // usage never kept
	} {
		turnAt(t, s, time.Duration(i+1)*time.Minute, TimerWakeSource, w.rounds, w.usage)
	}
	got, err := s.TimerWakeUsage(time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	want := WakeUsage{Complete: 4, Min: 0, Median: 100, Max: 300, Floors: 3, LargestFloor: 900, Unknown: 2}
	if got != want {
		t.Fatalf("wakes read as %+v, want %+v", got, want)
	}
}

// Timer wakes and nothing else, inside the window and nothing older. Every
// source a turn or a facility's call can carry is present with usage large
// enough to move any figure that let it in.
func TestTimerWakeUsageReadsOnlyTimerWakesInTheWindow(t *testing.T) {
	s := wakeStore(t)
	turnAt(t, s, time.Hour, TimerWakeSource, 2, tokens(1000, 0, 0))
	turnAt(t, s, 2*time.Hour, TimerWakeSource, 2, tokens(400, 1, 0))
	turnAt(t, s, 3*time.Hour, TimerWakeSource, 2, nil)
	for _, source := range []string{"operator", "steer", "arrival", "voice", "harvest", "continuation",
		FacilitySource("dream"), FacilitySource("timer"), "timers", "Timer", "timer ", ""} {
		turnAt(t, s, time.Hour, source, 5, tokens(700000, 0, 0))
		turnAt(t, s, time.Hour, source, 5, tokens(800000, 1, 0))
		turnAt(t, s, time.Hour, source, 5, nil)
	}
	turnAt(t, s, 47*time.Hour, TimerWakeSource, 2, tokens(6000, 0, 0)) // inside 48h
	turnAt(t, s, 49*time.Hour, TimerWakeSource, 2, tokens(9000, 0, 0)) // outside
	turnAt(t, s, 500*time.Hour, TimerWakeSource, 2, nil)

	got, err := s.TimerWakeUsage(48 * time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	want := WakeUsage{Complete: 2, Min: 1000, Median: 1000, Max: 6000, Floors: 1, LargestFloor: 400, Unknown: 1}
	if got != want {
		t.Fatalf("wakes read as %+v, want %+v", got, want)
	}
	if got, err = s.TimerWakeUsage(time.Minute); err != nil || got != (WakeUsage{}) {
		t.Fatalf("a window holding no wake reads %+v (%v), want the zero value", got, err)
	}
}

// The median is a total that was recorded: the lower middle of an even count
// and the middle of an odd one, whatever order the rows arrived in.
func TestTimerWakeUsageMedianIsARecordedTotal(t *testing.T) {
	for name, c := range map[string]struct {
		totals []int
		median int
	}{
		"one":  {[]int{7}, 7},
		"two":  {[]int{20, 10}, 10},
		"odd":  {[]int{30, 10, 20}, 20},
		"even": {[]int{40, 10, 30, 20}, 20},
		"ties": {[]int{5, 5, 5, 90}, 5},
	} {
		s := wakeStore(t)
		for i, total := range c.totals {
			turnAt(t, s, time.Duration(i+1)*time.Minute, TimerWakeSource, 1, tokens(total, 0, 0))
		}
		got, err := s.TimerWakeUsage(time.Hour)
		if err != nil || got.Median != c.median || got.Complete != len(c.totals) {
			t.Errorf("%s: %+v (%v), want median %d over %d wakes", name, got, err, c.median, len(c.totals))
		}
	}
}

// A row's usage is whole or NULL. A row that is part of each is read as
// unknown, never as a wake that reported nothing wrong.
func TestTimerWakeUsageNullIsUnknownNeverZero(t *testing.T) {
	s := wakeStore(t)
	for i, column := range []string{"total_tokens", "silent_calls", "unknown_attempts"} {
		at := time.Now().Add(-time.Duration(i+1) * time.Minute).UnixMilli()
		if err := s.InsertTurnMetric(TurnMetric{TsMs: at, Rounds: 2, Source: TimerWakeSource, Usage: tokens(1000, 0, 0)}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.DB().Exec(`UPDATE turn_metrics SET `+column+` = NULL WHERE ts_ms = ?`, at); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.TimerWakeUsage(time.Hour)
	if err != nil || got != (WakeUsage{Unknown: 3}) {
		t.Fatalf("part-NULL rows read as %+v (%v), want three unknown", got, err)
	}
}

// The SAFE mount of a database from before sources were kept has no source
// to test. It names no wake, and asking it is not an error.
func TestTimerWakeUsageOnAMountThatKeptNoSources(t *testing.T) {
	path := filepath.Join(t.TempDir(), "aii.db")
	s, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	olderRows(t, s, time.Now().UTC().UnixMilli())
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	older, err := OpenReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	defer older.Close()
	if !older.historicalTurnMetrics {
		t.Fatal("fixture: the mount kept its source column; it is not the older shape")
	}
	if got, err := older.TimerWakeUsage(48 * time.Hour); err != nil || got != (WakeUsage{}) {
		t.Fatalf("a mount with no sources read %+v (%v), want no wake and no error", got, err)
	}
}
