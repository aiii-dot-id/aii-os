package app

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/conversation"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// .
// .
// .
// .
func TestRecordTurnCostHandsRoundsToTheRow(t *testing.T) {
	dir := t.TempDir()
	st, err := store.New(filepath.Join(dir, "aii.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	a := &App{store: st}
	countSuccessfulCall(a, "grep", `{"pattern":"x"}`)
	countSuccessfulCall(a, "read", `{"file_path":"/tmp/x"}`)
	countSuccessfulCall(a, "read", `{"file_path":"/tmp/y"}`)

	// .
	a.recordTurnCost(conversation.TurnUsage{Calls: 2, TotalTokens: 30})

	var calls, rounds int
	if err := st.DB().QueryRow(`SELECT calls, rounds FROM turn_metrics ORDER BY ts_ms DESC LIMIT 1`).
		Scan(&calls, &rounds); err != nil {
		t.Fatalf("no turn row was minted: %v", err)
	}
	if calls != 3 || rounds != 2 {
		t.Fatalf("row holds (calls=%d, rounds=%d), want (3, 2) — the batch factor is not a single-row fact", calls, rounds)
	}

	// .
	a.turnMeterMu.Lock()
	left := a.turnRounds
	a.turnMeterMu.Unlock()
	if left != 0 {
		t.Fatalf("turnRounds = %d after the summary, want 0 — a stale rounds count would ride the next turn's row", left)
	}
}

// .
// .
// .
func TestRhythmBatchFactorExcludesPreInstrumentRows(t *testing.T) {
	dir := t.TempDir()
	st, err := store.New(filepath.Join(dir, "aii.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	now := time.Now().UTC().UnixMilli()
	for i, m := range []store.TurnMetric{
		{TsMs: now - 3000, Calls: 10, Rounds: 4},
		{TsMs: now - 2000, Calls: 50, Rounds: 0},
		{TsMs: now - 1000, Calls: 6, Rounds: 2},
	} {
		if err := st.InsertTurnMetric(m); err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
	}

	_, calls, _, _, _, batchCenti, err := st.RhythmStats(48 * time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 66 {
		t.Fatalf("calls = %d, want 66 — the headline sum still counts every row", calls)
	}
	// .
	// .
	if batchCenti != 266 {
		t.Fatalf("batch factor = %d centi-calls/round, want 266 — pre-instrument rows leaked into the ratio", batchCenti)
	}
}

// .
// .
func TestRhythmBatchFactorSilentWithoutInstrumentedRows(t *testing.T) {
	dir := t.TempDir()
	st, err := store.New(filepath.Join(dir, "aii.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	if err := st.InsertTurnMetric(store.TurnMetric{TsMs: time.Now().UTC().UnixMilli(), Calls: 12}); err != nil {
		t.Fatal(err)
	}
	_, _, _, _, _, batchCenti, err := st.RhythmStats(48 * time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if batchCenti != 0 {
		t.Fatalf("batch factor = %d with no instrumented rows, want 0 (the clause renders only above zero)", batchCenti)
	}
}

// .
// .
// .
// .
// .
// .
func TestCalibrationRecordsTheFirstTimelyDeclaration(t *testing.T) {
	dir := t.TempDir()
	st, err := store.New(filepath.Join(dir, "aii.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	a := &App{store: st}
	countSuccessfulCall(a, "read", `{"file_path":"/tmp/a"}`)
	countSuccessfulCall(a, "read", `{"file_path":"/tmp/b"}`)
	countSuccessfulCall(a, "work", `{"action":"update","steps":8}`)
	countSuccessfulCall(a, "read", `{"file_path":"/tmp/c"}`)
	countSuccessfulCall(a, "work", `{"action":"update","steps":1}`)

	a.turnMeterMu.Lock()
	live := a.turnPredicted
	a.turnMeterMu.Unlock()
	if live != 1 {
		t.Fatalf("live estimate = %d, want 1 — the cap reads the resident's current belief", live)
	}

	a.logTurnSummary()
	var predicted, ordinal int
	if err := st.DB().QueryRow(`SELECT predicted, declared_ordinal FROM turn_metrics ORDER BY ts_ms DESC LIMIT 1`).
		Scan(&predicted, &ordinal); err != nil {
		t.Fatal(err)
	}
	if predicted != 8 || ordinal != 3 {
		t.Fatalf("row holds (predicted=%d, ordinal=%d), want (8, 3) — the pair must describe one declaration event", predicted, ordinal)
	}
}
