package app

import (
	"context"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/conversation"
	"github.com/aiii-dot-id/aii-os/internal/interaction"
	"github.com/aiii-dot-id/aii-os/internal/llm"
)

// executeToolCall is the direct entry the behaviour tests use: what a tool
// does is what they ask, not who it is counted to, and no loop has
// recorded a start for them, so it resolves the seat at the call, as the
// plugin context's read does (callSeat). The loop's executor never comes
// here: appToolExecutor.Execute runs a call from the seat its start
// resolved (startedSeat, F1), and the seat's own tests drive that path
// (startedExecute).
func (a *App) executeToolCall(ctx context.Context, tc llm.ToolCall) conversation.Observation {
	return a.executeSeated(ctx, tc, a.callSeat(ctx))
}

// startedExecute runs one call as the loop does: its start recorded by
// actor's transcript at turn's ordinal 1, then between (where a second
// read of the active session could split the call), then the call run by
// the loop's executor from its start's seat. The completion is the
// caller's.
func startedExecute(t *testing.T, a *App, ctx context.Context, actor, turn string, tc llm.ToolCall, between func()) conversation.Observation {
	t.Helper()
	tc.EmissionOrdinal = 1
	tr := appTranscript{st: a.store, safe: a.safeTools, actor: actor}
	if err := tr.RecordToolStart(turn, 1, tc.ID, tc.Function.Name, tc.Function.Arguments, "m"); err != nil {
		t.Fatal(err)
	}
	if between != nil {
		between()
	}
	return appToolExecutor{a}.Execute(interaction.WithTurn(ctx, turn), tc)
}
