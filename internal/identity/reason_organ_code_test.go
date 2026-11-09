package identity

import (
	"fmt"
	"testing"
)

type organCoded struct{}

func (organCoded) Error() string      { return "you proved one 2 minutes ago" }
func (organCoded) ReasonCode() string { return "backup.spaced" }

// AN ORGAN'S ERROR THAT NAMES ITS OWN CODE IS RECORDED UNDER IT (the
// host, 2026-11-07: continuity's refusal fell back to the action's name).
func TestAnOrgansOwnCodeIsTheReasonCode(t *testing.T) {
	err := fmt.Errorf("work: %w", organCoded{})
	if got := ReasonCode("work", map[string]any{"action": "backup.verify"}, err); got != "backup.spaced" {
		t.Fatalf("got %q", got)
	}
	if got := ReasonCode("work", map[string]any{"action": "backup.verify"}, fmt.Errorf("plain")); got != "work.backup.verify" {
		t.Fatalf("a plain error: %q", got)
	}
}
