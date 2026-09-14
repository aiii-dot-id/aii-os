package app

import (
	"testing"
	"time"
)

// .
// .
// .
// .
func TestAPulseIsAcceptedOnlyWithOperatorInteraction(t *testing.T) {
	live := true
	var lastAct time.Time
	p := &dashboardPulse{
		live:     func() bool { return live },
		interval: 5 * time.Minute,
		active:   func(d time.Duration) bool { return !lastAct.IsZero() && time.Since(lastAct) <= d },
	}
	if p.Live() {
		t.Fatal("a live session with no interaction must not count as lived time")
	}
	lastAct = time.Now()
	if !p.Live() {
		t.Fatal("a session with the operator acting within the interval is lived time")
	}
	lastAct = time.Now().Add(-6 * time.Minute)
	if p.Live() {
		t.Fatal("an interaction older than the pulse interval does not carry a pulse")
	}
	// .
	live = false
	p.setOverride(true)
	if p.Live() {
		t.Fatal("foreground alone, with nobody acting, is not lived time")
	}
	lastAct = time.Now()
	if !p.Live() {
		t.Fatal("foreground with interaction is lived time")
	}
	// .
	p.setOverride(false)
	if p.Live() {
		t.Fatal("no session is never lived time")
	}
}
