package app

import (
	"context"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/identity"
	"github.com/aiii-dot-id/aii-os/internal/interaction"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// A CALL REJECTED BEFORE EFFECT IS RECORDED REFUSED: the work verb's
// promise rule refuses, and the observation says refused with the rule's
// code, so the status line names it among the refused and the summary
// counts it with the rest.
func TestARejectionBeforeEffectIsObservedAsRefused(t *testing.T) {
	a, _ := dispatchApp(t)
	defer stopApp(t, a)
	pressStage(t, a, store.StageTrusted)
	ctx := interaction.WithTurnSource(interaction.WithTurn(context.Background(), "turn_r"), turnSourceOperator)
	if _, err := a.engine.ExecuteAction(ctx, "verb", "work", map[string]any{"action": "start", "description": "a task"}); err != nil {
		t.Fatal(err)
	}
	ws, _ := a.store.ActiveWorkSession()
	obs := a.executeToolCall(ctx, scriptCall("c1", "work", map[string]any{"action": "update", "promise": identity.PromiseAlarmID(ws.ID)}))
	if !obs.Failed || !obs.Refused || obs.ReasonCode != "promise" || !strings.Contains(obs.Text, "it is this session's alarm") {
		t.Fatalf("a rejection before effect is not observed as refused under its code: %+v", obs)
	}
}
