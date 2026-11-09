package cognitive

import (
	"testing"
	"time"
)

// Held says "stall" only while a pass that advanced nothing is inside its
// spacing, from the stamp DREAM already keeps, and "" once the spacing has
// passed or when nothing stalled (F-COG-110).
func TestDreamHeldFollowsTheStallStampAndItsSpacing(t *testing.T) {
	d := &DreamFacility{}
	d.config.Store(&DreamConfig{Spacing: 10 * time.Minute})
	at := time.Date(2026, 10, 31, 11, 0, 0, 0, time.UTC)
	now := at
	d.now = func() time.Time { return now }

	if got := d.Held(); got != "" {
		t.Fatalf("nothing stalled, yet Held says %q", got)
	}
	d.stallMu.Lock()
	d.stalledAt = at
	d.stallMu.Unlock()
	now = at.Add(3 * time.Minute)
	if got := d.Held(); got != "stall" {
		t.Fatalf("inside the spacing Held must say stall, not %q", got)
	}
	now = at.Add(11 * time.Minute)
	if got := d.Held(); got != "" {
		t.Fatalf("after the spacing Held must say nothing, not %q", got)
	}
}

// TestHeldNamesTheBudgetWhenThePassFoundNoRoom is the reason a hold carries
// its cause (F-COG-110). A stall stamp alone says a pass advanced nothing;
// "stall" then leaves the operator guessing whether pressing again would
// help, when the pass that found no room already knew the answer. The pass
// records it, and the door reports it.
func TestHeldNamesTheBudgetWhenThePassFoundNoRoom(t *testing.T) {
	d := &DreamFacility{}
	d.config.Store(&DreamConfig{Spacing: 10 * time.Minute})
	at := time.Date(2026, 10, 31, 11, 0, 0, 0, time.UTC)
	now := at
	d.now = func() time.Time { return now }

	stamp := func(kind ConditionKind) {
		d.stallMu.Lock()
		d.stalledAt = at
		d.stallKind = kind
		d.stallMu.Unlock()
		now = at.Add(3 * time.Minute) // inside the spacing
	}

	// A pass that found no room: the operator is told the cause they can
	// act on, not a bare stall.
	stamp(ConditionNoRoom)
	if got := d.Held(); got != PressBudget {
		t.Fatalf("a stall the pass recorded as no-room must read as %q, not %q", PressBudget, got)
	}

	// A pass that stalled for any other cause leaves no reason behind, and
	// the hold must not claim the budget was the cause.
	stamp("")
	if got := d.Held(); got != PressStall {
		t.Fatalf("a stall with no recorded cause must read as %q, not %q", PressStall, got)
	}

	// Nor may another cause be reported as the budget.
	stamp(ConditionRefused)
	if got := d.Held(); got != PressStall {
		t.Fatalf("a stall refused for what it asked must read as %q, not %q", PressStall, got)
	}

	// And the reason must not outlive its spacing.
	now = at.Add(11 * time.Minute)
	if got := d.Held(); got != "" {
		t.Fatalf("after the spacing Held must say nothing, not %q", got)
	}
}
