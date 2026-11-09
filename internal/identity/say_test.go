package identity

import (
	"context"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/interaction"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// THE IDENTITY'S CHOICE ON AN UNPROMPTED WORD IS TYPED AND RECORDED IN ITS
// TURN: speak, hold (with the moment) or go_on; anything else is refused
// in the parameter's words; the host reads the choice by the turn.
func TestTheChoiceOnAnUnpromptedWordIsRecordedInItsTurn(t *testing.T) {
	e, st, _, _, _ := setupEngine(t)
	ctx := interaction.WithTurnSource(interaction.WithTurn(context.Background(), "turn-u"), "continuation")
	if _, err := e.ExecuteAction(ctx, "verb", "work", map[string]any{"action": "say", "choice": "later"}); err == nil || !strings.Contains(err.Error(), "is not one of speak, hold, go_on") {
		t.Fatalf("a choice outside the three was taken: %v", err)
	}
	out, err := e.ExecuteAction(ctx, "verb", "work", map[string]any{"action": "say", "choice": "hold", "until": "1800000000000"})
	if err != nil || !strings.Contains(out, "held until 1800000000000") {
		t.Fatalf("the hold's answer: %q %v", out, err)
	}
	choice, until, err := st.SayChoiceOf("turn-u")
	if err != nil || choice != "hold" || until != 1800000000000 {
		t.Fatalf("the hold is not read from the turn: %q %d %v", choice, until, err)
	}
	if _, err := e.ExecuteAction(ctx, "verb", "work", map[string]any{"action": "say", "choice": "speak"}); err != nil {
		t.Fatal(err)
	}
	if choice, _, _ := st.SayChoiceOf("turn-u"); choice != "speak" {
		t.Fatalf("the later choice is not the one read: %q", choice)
	}
	if choice, _, _ := st.SayChoiceOf("turn-other"); choice != "" {
		t.Fatalf("a turn that chose nothing reads %q", choice)
	}
	var n int
	_ = st.DB().QueryRow(`SELECT COUNT(*) FROM conversations WHERE turn_id='turn-u' AND json_extract(details,'$.reason_code')=?`, store.ReasonSayChoice).Scan(&n)
	if n != 2 {
		t.Fatalf("%d choice rows, want 2", n)
	}
}
