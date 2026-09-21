package app

import (
	"context"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"time"
)

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
var (
	harvestGateWait   = 30 * time.Second
	harvestTurnBudget = 15 * time.Minute
)

const harvestFusionInstruction = "Harvest it: state what the branches collectively establish, " +
	"reconcile it with your current plan, update your work state, and note " +
	"what deserves memory. Deliverables that fulfill a commitment are " +
	"yours to complete deliberately."

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
func (a *App) wakeSubagentDelivery(ctx context.Context, sessionID, goal string) {
	// .
	// .
	// .
	// .
	// .
	// .
	if a.currentMode() == ModeSafe {
		notice := fmt.Sprintf("[sub-agent %s delivered %s] — I am in safe mode: I woke, but I cannot write to my ledger until my operator restores it.",
			sessionID, time.Now().UTC().Format("15:04:05 MST Mon Jan 2"))
		wakeID := fmt.Sprintf("wake_subagent_%s_%d_safe", sessionID, time.Now().UTC().UnixNano())
		if a.dashboard != nil {
			if n := a.dashboard.PushTransient(wakeID, notice+" Goal: "+goal); n == 0 {
				logsink.Info("harvest.decision", "(SAFE): nobody connected — notice was transient-only: %s", notice)
			}
		} else {
			logsink.Info("harvest.decision", "(SAFE, no dashboard): %s %s", notice, goal)
		}
		return
	}

	notice := fmt.Sprintf("[sub-agent %s delivered %s]", sessionID,
		time.Now().UTC().Format("15:04:05 MST Mon Jan 2"))

	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	if err := a.acquireTurn(ctx); err != nil {
		logsink.Info("harvest.refusal", "%s: gate unavailable (mid-turn or coalesced): %v", sessionID, err)
		return
	}
	defer a.releaseTurn()

	// .
	// .
	// .
	// .
	// .
	// .
	if unharvested, err := a.store.UnharvestedDeliveries(1); err != nil {
		logsink.Warn("harvest.error", "%s: emptiness check failed (proceeding to harvest): %v", sessionID, err)
	} else if len(unharvested) == 0 {
		logsink.Info("harvest.refusal", "%s: coalesced — set already swept, standing down", sessionID)
		return
	}
	// .
	// .
	// .
	// .
	// .
	turnCtx, cancelTurn := context.WithTimeout(context.Background(), harvestTurnBudget)
	defer cancelTurn()
	spoken, err := a.wake(turnCtx, "system", notice+" — your sub-agent finished. "+harvestFusionInstruction)
	if err != nil {
		// .
		// .
		// .
		// .
		logsink.Warn("harvest.error", "%s: wake turn failed (outcome stays unharvested, resurfaces next turn): %v", sessionID, err)
	}
	if spoken == "" {
		return
	}
	wakeID := fmt.Sprintf("wake_subagent_%s_%d", sessionID, time.Now().UTC().UnixNano())
	if err := a.store.AddOutboxMessage(wakeID, "operator", "", spoken, nil); err != nil {
		logsink.Warn("harvest.error", "outbox write failed: %v", err)
	}
	logsink.Info("harvest.end", "%s: woke and spoke (%d chars)", sessionID, len(spoken))
}
