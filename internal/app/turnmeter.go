package app

import (
	"context"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"math"
	"strconv"
	"sync"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/conversation"
	"github.com/aiii-dot-id/aii-os/internal/interaction"
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

func withTurnSource(ctx context.Context, source string) context.Context {
	return interaction.WithTurnSource(ctx, source)
}

type turnState struct {
	mu sync.Mutex

	toolEmit func(kind, name, args string)

	replyEmit conversation.ReplyEmitter
	lastTurn  string

	accounted uint64

	requestSafe *bool

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

	legsQueued int

	askYields     int
	askFleetSpent bool

	legMeters map[string]*legMeter
}

func (a *App) recordTurnCost(ctx context.Context, result conversation.Result) {
	u := result.Usage
	if u.Calls == 0 {
		return
	}
	text := counted(u.TotalTokens, "token", "tokens") + " over " + counted(u.Calls, "call", "calls")
	if !u.Complete() {
		text = "at least " + text
	}
	if u.CachedPromptTokens > 0 || u.CacheReadReports == u.Calls {
		text += ", " + thousands(u.CachedPromptTokens) + " of it cached"
		if u.CacheReadReports == u.Calls && u.PromptTokens > 0 {
			text += fmt.Sprintf(" (%.1f%% of reported input)", 100*float64(u.CachedPromptTokens)/float64(u.PromptTokens))
		}
	}
	if u.CacheWriteTokens > 0 {
		text += ", " + counted(u.CacheWriteTokens, "cache-write token", "cache-write tokens")
	}
	if u.CacheWrite1hTokens > 0 {
		text += " (" + thousands(u.CacheWrite1hTokens) + " at 1h TTL)"
	}
	if u.Silent > 0 {
		text += fmt.Sprintf(" — %d reported nothing", u.Silent)
	}
	if u.UnknownAttempts > 0 {
		if u.UnknownAttempts == 1 {
			text += " — 1 earlier attempt has unknown usage"
		} else {
			text += " — " + thousands(u.UnknownAttempts) + " earlier attempts have unknown usage"
		}
	}
	if u.CacheReadReports < u.Calls {
		text += "; cache details incomplete"
	}
	logsink.Info("turn.budget", "Turn cost: %s (in %d, out %d)", text, u.PromptTokens, u.CompletionTokens)

	source := interaction.TurnSource(ctx)
	a.logTurnSummary(turnLoop{source: source, provider: a.currentProvider().Name, model: result.ModelID, turn: interaction.TurnID(ctx), usage: &u,
		emissions: &store.ToolCallEmissions{Count: result.ToolCallsEmitted, Complete: result.ToolCallCoverageComplete}, status: result.StatusLine, refused: result.RefusedCalls, done: result.CallsDone})
	a.turn.mu.Lock()
	a.turn.lastTurn = text
	a.turn.mu.Unlock()
}

func (a *App) countToolCall(name string, args map[string]any) int {
	ro := readOnlyToolCall(name, args)
	a.turn.mu.Lock()
	defer a.turn.mu.Unlock()
	a.turn.turnCalls++
	if ro {
		a.turn.turnReadOnly++
	}
	return a.turn.turnCalls
}

func (a *App) countSuccessfulWorkCall(args map[string]any, ordinal int) {
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

func workDeclaration(args map[string]any) (spawned bool, predicted, independent int) {
	switch action, _ := args["action"].(string); action {
	case "spawn":
		spawned = true
	case "update", "start", "":

		predicted, independent = declaredCount(args["steps"]), declaredCount(args["independent"])
	}
	return spawned, predicted, independent
}

func declaredCount(v any) int {
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

	turn      string
	usage     *conversation.TurnUsage
	emissions *store.ToolCallEmissions

	status string

	refused int

	done int
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

	status := ""
	if loop.status != "" {
		status = fmt.Sprintf(" status=%q", loop.status)
	}

	if loop.done > 0 {
		calls = loop.done
	}
	logsink.Info("turn.end", "TURN_SUMMARY calls=%d refused=%d read_only=%d spawned=%d harvested=%d predicted=%d declared_at=%d independent=%d rounds=%d%s", calls, loop.refused, ro, spawned, harvested, predicted, declaredAt, independent, rounds, status)

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
			TurnID:          loop.turn,
			Usage:           usage,
			Emissions:       loop.emissions,
		}); err != nil {

			logsink.Warn("turn.error", "turn metric not recorded: %v", err)
		} else {
			a.countAccounted()
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
		return
	}
	a.countAccounted()

	if a.dashboard != nil {
		a.dashboard.BroadcastStatus()
	}
}

func (a *App) countAccounted() {
	a.turn.mu.Lock()
	a.turn.accounted++
	a.turn.mu.Unlock()
}

func (a *App) accountedCount() uint64 {
	a.turn.mu.Lock()
	defer a.turn.mu.Unlock()
	return a.turn.accounted
}

func thousands(n int) string {
	s := strconv.Itoa(n)
	sign := ""
	if n < 0 {
		sign, s = "-", s[1:]
	}
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return sign + s
}

func counted(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return thousands(n) + " " + many
}

func (a *App) lastTurnCost() string {
	a.turn.mu.Lock()
	defer a.turn.mu.Unlock()
	return a.turn.lastTurn
}
