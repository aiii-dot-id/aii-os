package app

import (
	"context"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/llm"
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
func TestWorkFactsTakeTheirOrdinalFromEmissionNotCompletion(t *testing.T) {
	app, _ := dispatchApp(t)
	defer app.Stop()

	// .
	var start llm.ToolCall
	start.Function.Name = "work"
	start.Function.Arguments = `{"action":"start","goal":"prove the ordinal"}`
	start.EmissionOrdinal = 1
	if obs := app.executeToolCall(context.Background(), start); obs.Failed {
		t.Fatalf("could not start a work session: %s", obs.Text)
	}

	// .
	for i := 0; i < 3; i++ {
		var tc llm.ToolCall
		tc.Function.Name = "grep"
		tc.Function.Arguments = `{"pattern":"nothing-matches-this"}`
		tc.EmissionOrdinal = 90 + i
		app.executeToolCall(context.Background(), tc)
	}

	// .
	// .
	var work llm.ToolCall
	work.Function.Name = "work"
	work.Function.Arguments = `{"action":"update","steps":6}`
	work.EmissionOrdinal = 2
	if obs := app.executeToolCall(context.Background(), work); obs.Failed {
		t.Fatalf("the declaration itself failed: %s", obs.Text)
	}

	app.turnMeterMu.Lock()
	declared, predicted := app.turnDeclaredOrdinal, app.turnPredicted
	calls := app.turnCalls
	app.turnMeterMu.Unlock()

	if predicted != 6 {
		t.Fatalf("the declaration was not recorded at all: predicted=%d", predicted)
	}
	if declared != 2 {
		t.Errorf("declared ordinal = %d, want 2 (its EMITTED position). "+
			"A value near %d means the meter read the completion counter.", declared, calls)
	}
	// .
	if calls != 5 {
		t.Errorf("executed-call count = %d, want 5 — count and position are different facts", calls)
	}
}
