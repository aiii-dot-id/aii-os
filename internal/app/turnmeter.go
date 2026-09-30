package app

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"math"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/conversation"
	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

const (
	turnSourceOperator     = "operator"
	turnSourceSteer        = "steer"
	turnSourceArrival      = "arrival"
	turnSourceVoice        = "voice"
	turnSourceHarvest      = "harvest"
	turnSourceContinuation = "continuation"
)

const turnSourceTimer = store.TimerWakeSource

type turnSourceKey struct{}

func withTurnSource(ctx context.Context, source string) context.Context {
	return context.WithValue(ctx, turnSourceKey{}, source)
}

func (a *App) recordTurnCost(ctx context.Context, result conversation.Result) {
	u := result.Usage
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
	logsink.Info("turn.budget", "Turn cost: %s (in %d, out %d)", text, u.PromptTokens, u.CompletionTokens)

	source, _ := ctx.Value(turnSourceKey{}).(string)
	a.logTurnSummary(turnLoop{source: source, provider: a.currentProvider().Name, model: result.ModelID, usage: &u})
	a.turnCostMu.Lock()
	a.lastTurn = text
	a.turnCostMu.Unlock()
}

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

			a.turnFirstPredicted = predicted
		}
	}
	if independent > 0 && ordinal >= a.turnIndependentOrdinal {
		a.turnIndependent = independent
		a.turnIndependentOrdinal = ordinal
	}
}

func parseWorkDeclaration(argsJSON string) (spawned bool, predicted, independent int) {
	var w struct {
		Action string `json:"action"`

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

func (a *App) resetTurnMeter() {
	a.turnMeterMu.Lock()
	a.turnCalls, a.turnReadOnly, a.turnSpawned, a.turnHarvested, a.turnPredicted, a.turnIndependent, a.turnDeclaredOrdinal = 0, 0, 0, 0, 0, 0, 0
	a.turnFirstPredicted = 0
	a.turnPredictedOrdinal, a.turnIndependentOrdinal = 0, 0
	a.turnMeterMu.Unlock()
}

type turnLoop struct {
	source, provider, model string
	usage                   *conversation.TurnUsage
}

func (a *App) logTurnSummary(loop turnLoop) {

	a.turnMeterMu.Lock()
	calls, ro, spawned, harvested := a.turnCalls, a.turnReadOnly, a.turnSpawned, a.turnHarvested
	predicted, independent, declaredAt := a.turnFirstPredicted, a.turnIndependent, a.turnDeclaredOrdinal
	a.turnMeterMu.Unlock()
	rounds := 0
	var usage *store.TurnUsage
	if u := loop.usage; u != nil {
		rounds, usage = u.Calls, storedUsage(*u)
	}
	logsink.Info("turn.end", "TURN_SUMMARY calls=%d read_only=%d spawned=%d harvested=%d predicted=%d declared_at=%d independent=%d rounds=%d", calls, ro, spawned, harvested, predicted, declaredAt, independent, rounds)

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
			Source:          loop.source,
			Provider:        loop.provider,
			Model:           loop.model,
			Usage:           usage,
		}); err != nil {

			logsink.Warn("turn.error", "turn metric not recorded: %v", err)
		}
	}
	a.resetTurnMeter()
}

func storedUsage(u conversation.TurnUsage) *store.TurnUsage {
	return &store.TurnUsage{
		PromptTokens:       u.PromptTokens,
		CompletionTokens:   u.CompletionTokens,
		TotalTokens:        u.TotalTokens,
		CachedPromptTokens: u.CachedPromptTokens,
		CacheWriteTokens:   u.CacheWriteTokens,
		CacheWrite5mTokens: u.CacheWrite5mTokens,
		CacheWrite1hTokens: u.CacheWrite1hTokens,
		CacheReadReports:   u.CacheReadReports,
		CacheWriteReports:  u.CacheWriteReports,
		UnknownAttempts:    u.UnknownAttempts,
		Silent:             u.Silent,
	}
}

func (a *App) recordFacilityCall(facility, model string, u llm.Usage) {
	if a.store == nil {
		return
	}
	call := conversation.OneCall(u)
	if err := a.store.InsertTurnMetric(store.TurnMetric{
		TsMs:     time.Now().UTC().UnixMilli(),
		Rounds:   call.Calls,
		Source:   store.FacilitySource(facility),
		Provider: a.currentProvider().Name,
		Model:    model,
		Usage:    storedUsage(call),
	}); err != nil {
		logsink.Warn("turn.error", "facility call usage not recorded (%s): %v", facility, err)
	}
}

func (a *App) lastTurnCost() string {
	a.turnCostMu.Lock()
	defer a.turnCostMu.Unlock()
	return a.lastTurn
}
