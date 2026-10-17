package app

import (
	"context"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/llm"
)

func TestWorkFactsTakeTheirOrdinalFromEmissionNotCompletion(t *testing.T) {
	app, _ := dispatchApp(t)
	defer stopApp(t, app)

	// A session must exist before work can be updated.
	var start llm.ToolCall
	start.Function.Name = "work"
	start.Function.Arguments = `{"action":"start","goal":"prove the ordinal"}`
	start.EmissionOrdinal = 1
	if obs := app.executeToolCall(context.Background(), start); obs.Failed {
		t.Fatalf("could not start a work session: %s", obs.Text)
	}

	// Three read-only calls "finish" first, as a concurrent batch would.
	for i := 0; i < 3; i++ {
		var tc llm.ToolCall
		tc.Function.Name = "grep"
		tc.Function.Arguments = `{"pattern":"nothing-matches-this"}`
		tc.EmissionOrdinal = 90 + i // emitted late, finished early
		app.executeToolCall(context.Background(), tc)
	}

	// The declaration was emitted SECOND in its response, whatever the
	// completion order around it.
	var work llm.ToolCall
	work.Function.Name = "work"
	work.Function.Arguments = `{"action":"update","steps":6}`
	work.EmissionOrdinal = 2
	if obs := app.executeToolCall(context.Background(), work); obs.Failed {
		t.Fatalf("the declaration itself failed: %s", obs.Text)
	}

	app.turn.mu.Lock()
	declared, predicted := app.turn.turnDeclaredOrdinal, app.turn.turnPredicted
	calls := app.turn.turnCalls
	app.turn.mu.Unlock()

	if predicted != 6 {
		t.Fatalf("the declaration was not recorded at all: predicted=%d", predicted)
	}
	if declared != 2 {
		t.Errorf("declared ordinal = %d, want 2 (its EMITTED position). "+
			"A value near %d means the meter read the completion counter.", declared, calls)
	}
	// And the execution COUNT is still its own fact, unharmed.
	if calls != 5 {
		t.Errorf("executed-call count = %d, want 5 — count and position are different facts", calls)
	}
}
