package identity

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/interaction"
)

// A MOMENT ALREADY PAST IS NOT A WAIT (two yields named the right time of
// day in the wrong year's milliseconds and the identity sat idle): a time condition must be after now, the refusal
// spells both moments out, and an accepted one is read back in words
// beside its number, in the yield's reply and the update's.
func TestATimeConditionMustBeAfterNowAndIsReadBackInWords(t *testing.T) {
	engine, _, _, _, _ := setupEngine(t)
	ctx := interaction.WithTurnSource(interaction.WithTurn(context.Background(), "turn_op"), "operator")
	if _, err := engine.ExecuteAction(ctx, "verb", "work", map[string]any{"action": "start", "description": "a long task"}); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-365 * 24 * time.Hour).UnixMilli()
	_, err := engine.ExecuteAction(ctx, "verb", "work", map[string]any{"action": "yield", "resumes_on": fmt.Sprintf("time:%d", past)})
	if err == nil {
		t.Fatal("a moment a year past was accepted")
	}
	want := fmt.Sprintf("time:%d is %s, which is in the past; now is ", past, time.UnixMilli(past).UTC().Format(time.RFC3339))
	if !strings.Contains(err.Error(), want) || !strings.Contains(err.Error(), "Name a moment after now in unix ms") {
		t.Fatalf("the refusal does not spell the moments out: %v", err)
	}
	future := time.Now().Add(time.Hour).UnixMilli()
	words := time.UnixMilli(future).UTC().Format(time.RFC3339)
	out, err := engine.ExecuteAction(ctx, "verb", "work", map[string]any{"action": "update", "resumes_on": fmt.Sprintf("time:%d", future)})
	if err != nil || !strings.Contains(out, fmt.Sprintf("You resume at %s (%d).", words, future)) {
		t.Fatalf("the update does not read the moment back: %q %v", out, err)
	}
	out, err = engine.ExecuteAction(ctx, "verb", "work", map[string]any{"action": "yield", "resumes_on": fmt.Sprintf("time:%d", future)})
	if err != nil || !strings.Contains(out, fmt.Sprintf("you resume at %s (%d).", words, future)) {
		t.Fatalf("the yield does not read the moment back: %q %v", out, err)
	}
}
