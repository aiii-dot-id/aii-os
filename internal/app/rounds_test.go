package app

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/conversation"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

func TestRecordTurnCostHandsRoundsToTheRow(t *testing.T) {
	dir := t.TempDir()
	st, err := store.NewForTest(filepath.Join(dir, "aii.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	a := &App{store: st}
	countSuccessfulCall(a, "grep", `{"pattern":"x"}`)
	countSuccessfulCall(a, "read", `{"file_path":"/tmp/x"}`)
	countSuccessfulCall(a, "read", `{"file_path":"/tmp/y"}`)

	// Three tool calls over two model rounds: a batched turn.
	a.recordTurnCost(t.Context(), conversation.Result{Usage: conversation.TurnUsage{Calls: 2, TotalTokens: 30}})

	var calls, rounds int
	if err := st.DB().QueryRow(`SELECT calls, rounds FROM turn_metrics ORDER BY ts_ms DESC LIMIT 1`).
		Scan(&calls, &rounds); err != nil {
		t.Fatalf("no turn row was minted: %v", err)
	}
	if calls != 3 || rounds != 2 {
		t.Fatalf("row holds (calls=%d, rounds=%d), want (3, 2) — the batch factor is not a single-row fact", calls, rounds)
	}

	// Rounds ride the handover, not the meter: the next summary, with no
	// loop behind it, cannot carry this turn's count into its own row.
	a.logTurnSummary(turnLoop{})
	if err := st.DB().QueryRow(`SELECT rounds FROM turn_metrics ORDER BY ts_ms DESC LIMIT 1`).Scan(&rounds); err != nil {
		t.Fatal(err)
	}
	if rounds != 0 {
		t.Fatalf("rounds = %d on the next row, want 0 — a stale rounds count rode the next turn's row", rounds)
	}
}

// The rhythm line's batch clause: measured, no verdict, silent until
// instrumented rows exist — and pre-instrument rows (rounds=0) stay
// out of BOTH sides of the ratio, or migrated history would inflate it.
func TestRhythmBatchFactorExcludesPreInstrumentRows(t *testing.T) {
	dir := t.TempDir()
	st, err := store.NewForTest(filepath.Join(dir, "aii.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	now := time.Now().UTC().UnixMilli()
	for i, m := range []store.TurnMetric{
		{TsMs: now - 3000, Calls: 10, Rounds: 4}, // instrumented, batched
		{TsMs: now - 2000, Calls: 50, Rounds: 0}, // pre-instrument row
		{TsMs: now - 1000, Calls: 6, Rounds: 2},  // instrumented
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
	// Matched numerator: (10+6) calls over (4+2) rounds = 2.66/round.
	// Including the pre-instrument row's 50 calls would claim 11/round.
	if batchCenti != 266 {
		t.Fatalf("batch factor = %d centi-calls/round, want 266 — pre-instrument rows leaked into the ratio", batchCenti)
	}
}

// No instrumented rows at all: the factor is zero and the rhythm line
// stays silent about batching rather than reporting a fabricated 0.0.
func TestRhythmBatchFactorSilentWithoutInstrumentedRows(t *testing.T) {
	dir := t.TempDir()
	st, err := store.NewForTest(filepath.Join(dir, "aii.db"))
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

func TestCalibrationRecordsTheFirstTimelyDeclaration(t *testing.T) {
	dir := t.TempDir()
	st, err := store.NewForTest(filepath.Join(dir, "aii.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	a := &App{store: st}
	countSuccessfulCall(a, "read", `{"file_path":"/tmp/a"}`)
	countSuccessfulCall(a, "read", `{"file_path":"/tmp/b"}`)
	countSuccessfulCall(a, "work", `{"action":"update","steps":8}`) // call 3: the declaration
	countSuccessfulCall(a, "read", `{"file_path":"/tmp/c"}`)
	countSuccessfulCall(a, "work", `{"action":"update","steps":1}`) // finish-line restatement

	a.turn.mu.Lock()
	live := a.turn.turnPredicted
	a.turn.mu.Unlock()
	if live != 1 {
		t.Fatalf("live estimate = %d, want 1 — the cap reads the resident's current belief", live)
	}

	a.logTurnSummary(turnLoop{})
	var predicted, ordinal int
	if err := st.DB().QueryRow(`SELECT predicted, declared_ordinal FROM turn_metrics ORDER BY ts_ms DESC LIMIT 1`).
		Scan(&predicted, &ordinal); err != nil {
		t.Fatal(err)
	}
	if predicted != 8 || ordinal != 3 {
		t.Fatalf("row holds (predicted=%d, ordinal=%d), want (8, 3) — the pair must describe one declaration event", predicted, ordinal)
	}
}
