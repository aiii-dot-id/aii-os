package app

import (
	"errors"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/cognitive"
)

// The press refuses what the app knows before it asks TIME, each reason
// typed, and carries TIME's own reasons through unchanged (F-COG-110).
func TestPassNowRefusesWhatTheAppKnowsBeforeAskingTIME(t *testing.T) {
	asked := 0
	moved := time.Date(2026, 10, 31, 11, 30, 0, 250_000_000, time.UTC).UnixMilli()
	bring := func() (cognitive.BringForwardResult, error) {
		asked++
		return cognitive.BringForwardResult{Moved: true, From: moved + 3_600_000, To: moved}, nil
	}

	res, err := passNowDecision(func() bool { return true }, func() string { return "" }, bring)
	if err != nil || res.Moved || res.Reason != "turn_holding" || res.AskedAt != "" {
		t.Fatalf("a foreground turn holding must refuse turn_holding before TIME is asked: %+v %v", res, err)
	}
	res, err = passNowDecision(func() bool { return false }, func() string { return "stall" }, bring)
	if err != nil || res.Moved || res.Reason != "stall" {
		t.Fatalf("a stalled DREAM must refuse stall before TIME is asked: %+v %v", res, err)
	}
	if asked != 0 {
		t.Fatalf("TIME was asked %d time(s) while the app itself refused", asked)
	}
	res, err = passNowDecision(func() bool { return false }, func() string { return "" }, bring)
	if err != nil || !res.Moved || res.Reason != "" || res.AskedAt != "2026-10-31T11:30:00.250Z" {
		t.Fatalf("a clear press must move and say TIME's own clock at the move (To), RFC 3339 UTC to the millisecond — not a later sample: %+v %v", res, err)
	}
}

// TIME's refusals reach the page as TIME names them; a store error is an
// error, not a refusal.
func TestPassNowCarriesTIMEsReasonsThrough(t *testing.T) {
	for _, reason := range []string{cognitive.PressSafe, cognitive.PressInFlight, cognitive.PressPaused, cognitive.PressAlreadyDue, cognitive.PressChanged, cognitive.PressNotRecurring, cognitive.PressAbsent} {
		res, err := passNowDecision(func() bool { return false }, func() string { return "" }, func() (cognitive.BringForwardResult, error) {
			return cognitive.BringForwardResult{Reason: reason}, nil
		})
		if err != nil || res.Moved || res.Reason != reason || res.AskedAt != "" {
			t.Fatalf("TIME's %q must reach the page unchanged: %+v %v", reason, res, err)
		}
	}
	_, err := passNowDecision(func() bool { return false }, func() string { return "" }, func() (cognitive.BringForwardResult, error) {
		return cognitive.BringForwardResult{}, errors.New("read alarm rhythm: disk gone")
	})
	if err == nil {
		t.Fatal("a store error must be returned as an error, not said as a refusal")
	}
}
