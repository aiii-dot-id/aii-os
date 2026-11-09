package identity

import (
	"context"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/interaction"
)

// A DELIVERY THAT CAN NEVER COME IS NOT A WAIT (a review, 2026-11-08, D6): the
// identity's update naming the session itself, or a session that does not
// exist, is refused before anything of the call is written, saying why and
// what can resume the work; the update's plan is left as it was.
func TestADeliveryThatCanNeverComeIsRefusedBeforeAnythingIsWritten(t *testing.T) {
	engine, st, _, _, _ := setupEngine(t)
	ctx := interaction.WithTurnSource(interaction.WithTurn(context.Background(), "turn_op"), "operator")
	if _, err := engine.ExecuteAction(ctx, "verb", "work", map[string]any{"action": "start", "description": "a long task"}); err != nil {
		t.Fatal(err)
	}
	ws, err := st.ActiveWorkSession()
	if err != nil || ws == nil {
		t.Fatalf("no session: %v", err)
	}
	for on, why := range map[string]string{
		"delivery:" + ws.ID:   "is this work session",
		"delivery:ws_nowhere": "no work session ws_nowhere exists",
	} {
		_, err := engine.ExecuteAction(ctx, "verb", "work", map[string]any{"action": "update", "focus": "a focus that must not be written", "resumes_on": on})
		if err == nil || !strings.Contains(err.Error(), "a delivery can never come: ") || !strings.Contains(err.Error(), why) || !strings.Contains(err.Error(), "or name word, time:<unix ms> or now") {
			t.Fatalf("%s: %v; want it refused, saying %q and what can resume the work", on, err, why)
		}
		after, err := st.WorkSessionByID(ws.ID)
		if err != nil || after.Focus != "" || after.ResumesOn != "" {
			t.Fatalf("%s: the refused update wrote something: focus %q, resumes_on %q (%v)", on, after.Focus, after.ResumesOn, err)
		}
	}
}
