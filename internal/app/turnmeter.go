package app

import (
	"context"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"math"
	"sync"
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

type turnState struct {
	mu sync.Mutex

	toolEmit func(kind, name, args string)
	lastTurn string

	turnCalls    int
	turnReadOnly int
	turnSpawned  int

	turnHarvested int

	turnPredicted int

	turnPredictedOrdinal   int
	turnIndependentOrdinal int

	turnFirstPredicted int

	turnDeclaredOrdinal int

	turnIndependent int

	contChain int

	askYields     int
	askFleetSpent bool

	legMeters map[string]*legMeter
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
	a.turn.mu.Lock()
	a.turn.lastTurn = text
	a.turn.mu.Unlock()
}

func (a *App) countToolCall(name string, args map[string]interface{}) int {
	ro := readOnlyToolCall(name, args)
	a.turn.mu.Lock()
	defer a.turn.mu.Unlock()
	a.turn.turnCalls++
	if ro {
		a.turn.turnReadOnly++
	}
	return a.turn.turnCalls
}

func (a *App) countSuccessfulWorkCall(args map[string]interface{}, ordinal int) {
	spawned, predicted, independent := workDeclaration(args)
	a.turn.mu.Lock()
	defer a.turn.mu.Unlock()
	if spawned {
		a.turn.turnSpawned++
	}
	if predicted > 0 {
		if ordinal >= a.turn.turnPredictedOrdinal {
			a.turn.turnPredicted = predicted
			a.turn.turnPredictedOrdinal = ordinal
		}
		if a.turn.turnDeclaredOrdinal == 0 || ordinal < a.turn.turnDeclaredOrdinal {
			a.turn.turnDeclaredOrdinal = ordinal

			a.turn.turnFirstPredicted = predicted
		}
	}
	if independent > 0 && ordinal >= a.turn.turnIndependentOrdinal {
		a.turn.turnIndependent = independent
		a.turn.turnIndependentOrdinal = ordinal
	}
}

func workDeclaration(args map[string]interface{}) (spawned bool, predicted, independent int) {
	switch action, _ := args["action"].(string); action {
	case "spawn":
		spawned = true
	case "update":
		predicted, independent = declaredCount(args["steps"]), declaredCount(args["independent"])
	}
	return spawned, predicted, independent
}

func declaredCount(v interface{}) int {
	f, ok := v.(float64)
	if !ok || f <= 0 || f != math.Trunc(f) {
		return 0
	}
	return int(f)
}

func (a *App) resetTurnMeter() {
	a.turn.mu.Lock()
	a.turn.turnCalls, a.turn.turnReadOnly, a.turn.turnSpawned, a.turn.turnHarvested, a.turn.turnPredicted, a.turn.turnIndependent, a.turn.turnDeclaredOrdinal = 0, 0, 0, 0, 0, 0, 0
	a.turn.turnFirstPredicted = 0
	a.turn.turnPredictedOrdinal, a.turn.turnIndependentOrdinal = 0, 0
	a.turn.mu.Unlock()
}

type turnLoop struct {
	source, provider, model string
	usage                   *conversation.TurnUsage
}

func (a *App) logTurnSummary(loop turnLoop) {

	a.turn.mu.Lock()
	calls, ro, spawned, harvested := a.turn.turnCalls, a.turn.turnReadOnly, a.turn.turnSpawned, a.turn.turnHarvested
	predicted, independent, declaredAt := a.turn.turnFirstPredicted, a.turn.turnIndependent, a.turn.turnDeclaredOrdinal
	a.turn.mu.Unlock()
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
	a.turn.mu.Lock()
	defer a.turn.mu.Unlock()
	return a.turn.lastTurn
}
