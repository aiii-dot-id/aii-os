package app

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/conversation"
	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// What the recurring set's line counts is what the
// app records: one spelling of the source, read from the row a real wake
// wrote, and the loop's own rule for a total that is exact.

// A wake through the entry the timer really takes leaves a row that the
// store's reader finds, beside rows of other paths and of a facility's call
// that it does not.
func TestTimerWakeUsageReadsWhatARealWakeRecords(t *testing.T) {
	a := meteredApp(t, func(w http.ResponseWriter, r *http.Request) {
		response := map[string]any{"content": []any{map[string]any{"type": "text", "text": "The kettle."}}, "stop_reason": "end_turn", "usage": map[string]any{"input_tokens": 40, "cache_read_input_tokens": 0, "output_tokens": 3}}
		if err := json.NewEncoder(w).Encode(response); err != nil {
			t.Error(err)
		}
	})
	a.wakeTimerAlarm(t.Context(), "alarm_fixture", "the kettle's alarm fired")
	wake := lastTurnRow(t, a.store)
	if wake.source.String != turnSourceTimer || !wake.usage.Complete() || wake.usage.TotalTokens == 0 {
		t.Fatalf("fixture: the wake left %+v", wake)
	}
	big := conversation.TurnUsage{Calls: 2, PromptTokens: 700000, TotalTokens: 700000}
	a.recordTurnCost(withTurnSource(t.Context(), turnSourceOperator), conversation.Result{Usage: big, ModelID: "m"})
	a.recordFacilityCall("dream", "m", llm.Usage{PromptTokens: 800000, TotalTokens: 800000, Reported: true})

	got, err := a.store.TimerWakeUsage(time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	total := wake.usage.TotalTokens
	if want := (store.WakeUsage{Complete: 1, Min: total, Median: total, Max: total}); got != want {
		t.Fatalf("the reader found %+v, want the one wake %+v", got, want)
	}
	if turnSourceTimer != store.TimerWakeSource {
		t.Fatalf("the app records wakes under %q; the store reads %q", turnSourceTimer, store.TimerWakeSource)
	}
}

// The reader's completeness is conversation.TurnUsage.Complete's rule and
// not a copy that can drift from it: each usage the loop can hand over is
// recorded by the app's own writer, and the reader puts the wake where the
// loop's rule says — exact when Complete, a floor when not and something was
// reported, unknown when nothing was.
func TestTimerWakeUsageIsExactByTheLoopsRule(t *testing.T) {
	base := conversation.TurnUsage{PromptTokens: 1001, CompletionTokens: 1002, TotalTokens: 1003, Calls: 3}
	with := func(edit func(*conversation.TurnUsage)) conversation.TurnUsage { u := base; edit(&u); return u }
	for name, usage := range map[string]conversation.TurnUsage{
		"every call reported":            base,
		"a silent call":                  with(func(u *conversation.TurnUsage) { u.Silent = 1 }),
		"an attempt of unknown usage":    with(func(u *conversation.TurnUsage) { u.UnknownAttempts = 2 }),
		"a silent call and an attempt":   with(func(u *conversation.TurnUsage) { u.Silent, u.UnknownAttempts = 1, 1 }),
		"every call silent":              {Calls: 2, Silent: 2},
		"one call that reported nothing": {Calls: 1, Silent: 1},
		"a call that reported zero":      {Calls: 1},
	} {
		t.Run(name, func(t *testing.T) {
			st, err := store.NewForTest(filepath.Join(t.TempDir(), "aii.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { st.Close() })
			a := &App{store: st}
			a.adoptProvider(providerEntry{Name: "fixture-provider"})
			a.recordTurnCost(withTurnSource(t.Context(), turnSourceTimer), conversation.Result{Usage: usage, ModelID: "fixture-model"})

			want := store.WakeUsage{Unknown: 1}
			switch {
			case usage.Complete():
				want = store.WakeUsage{Complete: 1, Min: usage.TotalTokens, Median: usage.TotalTokens, Max: usage.TotalTokens}
			case usage.TotalTokens > 0:
				want = store.WakeUsage{Floors: 1, LargestFloor: usage.TotalTokens}
			}
			got, err := st.TimerWakeUsage(time.Hour)
			if err != nil || got != want {
				t.Fatalf("Complete() = %v: the reader found %+v (%v), want %+v", usage.Complete(), got, err, want)
			}
		})
	}
}
