package cognitive

import (
	"context"
	"testing"
	"time"
)

// rhythm_spacing_test.go — spacing advances on ATTEMPT, success or not.
//
// This file asserted the opposite for exactly one morning (2026-08-26:
// advance on success only, so a transport timeout would not cost the
// portrait its six-hour window). By afternoon a self_model pass with a
// persistently refused citation was re-running on EVERY ten-minute
// tick, holding the turn gate for minutes each time, and the
// operator's messages queued behind metabolism — a live outage. A
// failed pass now waits its full spacing like a successful one; the
// failure stays visible as the raw experience the facility mints
// (self_model.go), not as a retry loop with the identity in its hand.

type moodyOwner struct {
	name   string
	accept bool
	runs   int
}

func (m *moodyOwner) Name() string { return m.name }
func (m *moodyOwner) OnAlarm(context.Context, string, string, int64, string) AlarmResult {
	m.runs++
	return AlarmResult{Accepted: m.accept}
}

func TestAFailedReflectivePassSpendsItsSpacingAnyway(t *testing.T) {
	sm := &moodyOwner{name: "self_model", accept: false}
	r := NewRhythm(&fakeRaw{n: 0}, freeGate(), nil, nil, sm, nil)
	r.lastSelfModel = time.Now().Add(-7 * time.Hour) // due
	r.lastReview = time.Now()
	r.lastConsolidate = time.Now()

	r.OnAlarm(context.Background(), "rhythm", "wall", 0, "")
	if sm.runs != 1 {
		t.Fatalf("due pass did not run: %d", sm.runs)
	}
	// THE CLOCK MOVED ON THE ATTEMPT: the next tick must not retry. A
	// persistent failure retried every tick is a storm with the turn
	// gate in its hand (outage 2026-08-26 13:08).
	r.OnAlarm(context.Background(), "rhythm", "wall", 0, "")
	if sm.runs != 1 {
		t.Fatalf("failed pass retried inside its spacing: runs=%d", sm.runs)
	}

	// When the window genuinely elapses again, it runs again.
	r.lastSelfModel = time.Now().Add(-7 * time.Hour)
	r.OnAlarm(context.Background(), "rhythm", "wall", 0, "")
	if sm.runs != 2 {
		t.Fatalf("next due window did not run: %d", sm.runs)
	}
}
