package app

import (
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/foreground"
)

// .
// .
// .
func TestTurnGripsForegroundNeed(t *testing.T) {
	a := gateApp()
	a.fg = &foreground.Holds{}

	if !a.TryBeginTurn() {
		t.Fatal("free gate refused")
	}
	got := a.fg.Active()
	if len(got) != 1 || got[0] != "turn" {
		t.Fatalf("a held gate must grip the foreground, got %v", got)
	}
	a.releaseTurn()
	if got := a.fg.Active(); len(got) != 0 {
		t.Fatalf("the grip outlived its turn: %v", got)
	}
}
