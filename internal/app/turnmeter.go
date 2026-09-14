// .
// .
// .
package app

import (
	"encoding/json"
	"fmt"
	"log"
	"math"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/conversation"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// .
// .
// .
// .
// .
// .
// .
func (a *App) recordTurnCost(u conversation.TurnUsage) {
	if u.Calls == 0 {
		return
	}
	text := fmt.Sprintf("%d tokens over %d call(s)", u.TotalTokens, u.Calls)
	if !u.Complete() {
		text = "at least " + text
	}
	if u.CachedPromptTokens > 0 || u.CacheReadReports == u.Calls {
		text += fmt.Sprintf(", %d of it cached", u.CachedPromptTokens)
		if u.CacheReadReports == u.Calls && u.PromptTokens > 0 {
			text += fmt.Sprintf(" (%.1f%% of reported input)", 100*float64(u.CachedPromptTokens)/float64(u.PromptTokens))
		}
	}
	if u.CacheWriteTokens > 0 {
		text += fmt.Sprintf(", %d cache-write tokens", u.CacheWriteTokens)
	}
	if u.CacheWrite1hTokens > 0 {
		text += fmt.Sprintf(" (%d at 1h TTL)", u.CacheWrite1hTokens)
	}
	if u.Silent > 0 {
		text += fmt.Sprintf(" — %d reported nothing", u.Silent)
	}
	if u.UnknownAttempts > 0 {
		text += fmt.Sprintf(" — %d earlier attempt(s) have unknown usage", u.UnknownAttempts)
	}
	if u.CacheReadReports < u.Calls {
		text += "; cache details incomplete"
	}
	log.Printf("Turn cost: %s (in %d, out %d)", text, u.PromptTokens, u.CompletionTokens)
	// .
	// .
	// .
	a.turnMeterMu.Lock()
	a.turnRounds = u.Calls
	a.turnMeterMu.Unlock()
	a.logTurnSummary()
	a.turnCostMu.Lock()
	a.lastTurn = text
	a.turnCostMu.Unlock()
}

// .
// .
// .
func (a *App) countToolCall(name, argsJSON string) int {
	ro := readOnlyToolCall(name, argsJSON)
	a.turnMeterMu.Lock()
	defer a.turnMeterMu.Unlock()
	a.turnCalls++
	if ro {
		a.turnReadOnly++
	}
	return a.turnCalls
}

// .
// .
// .
func (a *App) countSuccessfulWorkCall(argsJSON string, ordinal int) {
	spawned, predicted, independent := parseWorkDeclaration(argsJSON)
	a.turnMeterMu.Lock()
	defer a.turnMeterMu.Unlock()
	if spawned {
		a.turnSpawned++
	}
	if predicted > 0 {
		if ordinal >= a.turnPredictedOrdinal {
			a.turnPredicted = predicted
			a.turnPredictedOrdinal = ordinal
		}
		if a.turnDeclaredOrdinal == 0 || ordinal < a.turnDeclaredOrdinal {
			a.turnDeclaredOrdinal = ordinal
			// .
			// .
			// .
			// .
			// .
			a.turnFirstPredicted = predicted
		}
	}
	if independent > 0 && ordinal >= a.turnIndependentOrdinal {
		a.turnIndependent = independent
		a.turnIndependentOrdinal = ordinal
	}
}

// .
// .
// .
func parseWorkDeclaration(argsJSON string) (spawned bool, predicted, independent int) {
	var w struct {
		Action string `json:"action"`
		// .
		// .
		Steps       *float64 `json:"steps"`
		Independent *float64 `json:"independent"`
	}
	if json.Unmarshal([]byte(argsJSON), &w) != nil {
		return
	}
	if w.Action == "spawn" {
		spawned = true
	}
	if w.Action == "update" {
		if w.Steps != nil && *w.Steps > 0 && *w.Steps == math.Trunc(*w.Steps) {
			predicted = int(*w.Steps)
		}
		if w.Independent != nil && *w.Independent > 0 && *w.Independent == math.Trunc(*w.Independent) {
			independent = int(*w.Independent)
		}
	}
	return spawned, predicted, independent
}

// .
func (a *App) resetTurnMeter() {
	a.turnMeterMu.Lock()
	a.turnCalls, a.turnReadOnly, a.turnSpawned, a.turnHarvested, a.turnPredicted, a.turnIndependent, a.turnDeclaredOrdinal = 0, 0, 0, 0, 0, 0, 0
	a.turnRounds, a.turnFirstPredicted = 0, 0
	a.turnPredictedOrdinal, a.turnIndependentOrdinal = 0, 0
	a.turnMeterMu.Unlock()
}

// .
// .
// .
func (a *App) logTurnSummary() {
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
	a.turnMeterMu.Lock()
	calls, ro, spawned, harvested := a.turnCalls, a.turnReadOnly, a.turnSpawned, a.turnHarvested
	predicted, independent, declaredAt := a.turnFirstPredicted, a.turnIndependent, a.turnDeclaredOrdinal
	rounds := a.turnRounds
	a.turnMeterMu.Unlock()
	log.Printf("TURN_SUMMARY calls=%d read_only=%d spawned=%d harvested=%d predicted=%d declared_at=%d independent=%d rounds=%d", calls, ro, spawned, harvested, predicted, declaredAt, independent, rounds)
	// .
	// .
	// .
	// .
	// .
	// .
	if a.store != nil {
		if err := a.store.InsertTurnMetric(store.TurnMetric{
			TsMs:            time.Now().UTC().UnixMilli(),
			Calls:           calls,
			ReadOnly:        ro,
			Spawned:         spawned,
			Harvested:       harvested,
			Predicted:       predicted,
			DeclaredOrdinal: declaredAt,
			Independent:     independent,
			Rounds:          rounds,
		}); err != nil {
			// .
			// .
			// .
			log.Printf("Warning: turn metric not recorded: %v", err)
		}
	}
	a.resetTurnMeter()
}

func (a *App) lastTurnCost() string {
	a.turnCostMu.Lock()
	defer a.turnCostMu.Unlock()
	return a.lastTurn
}
