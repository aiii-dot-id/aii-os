package app

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
)

// The next turn may take the token and install its speech binding before
// the previous releaseTurn returns. Force that schedule at the handoff
// step, using the same capacity-one gate and admission as production.
func TestVoiceTurnHandoffKeepsSuccessorReplyAndFinishHold(t *testing.T) {
	for _, oldUnanswered := range []bool{false, true} {
		name := "previous turn already settled"
		if oldUnanswered {
			name = "previous turn lost its reply"
		}
		t.Run(name, func(t *testing.T) {
			a := gateApp()
			a.cfg = &Config{} // an unset voice mode is auto: today's behaviour
			if !a.TryBeginTurn() {
				t.Fatal("previous turn could not take the gate")
			}
			var oldReleased, nextReleased atomic.Int32
			if oldUnanswered {
				a.holdVoice(&voiceBinding{session: "previous", done: func() { oldReleased.Add(1) }})
			}
			h, engine := newTrackedSession(a, "vs-next", true)
			binding := a.voiceBindingFor(heardUtterance{SessionID: h.id, Gen: h.gen.Load()})
			giveBack := binding.done
			binding.done = func() { nextReleased.Add(1); giveBack() }
			a.turnAfterHandoff = func() {
				if !a.TryBeginTurn() {
					t.Fatal("successor could not take the returned token")
				}
				a.holdVoice(binding)
			}
			a.releaseTurn()
			a.turnAfterHandoff = nil
			defer a.releaseTurn()
			if got := nextReleased.Load(); got != 0 {
				t.Errorf("previous turn erased the successor's local reply and released its Finish hold: releases=%d", got)
			}
			wantOld := int32(0)
			if oldUnanswered {
				wantOld = 1
			}
			if got := oldReleased.Load(); got != wantOld {
				t.Errorf("ending turn's own unanswered binding releases=%d, want %d", got, wantOld)
			}
			// Exercise actual local TTS admission and the actual Finish waiter.
			// Only the expensive engine is a fake; the host custody path is real.
			a.voiceInputFinished(h.id)
			a.settleVoice(context.Background(), "The next turn's complete local reply.")
			awaitDrained(t, h)
			if got := strings.Join(engine.opsSeen(), ","); got != "synthesize:vs-next,close:vs-next" {
				t.Errorf("successor must speak once before its drain; operations=%q", got)
			}
			if got := nextReleased.Load(); got != 1 {
				t.Errorf("successor binding releases=%d, want exactly one", got)
			}
			binding.release("duplicate cleanup")
			if nextReleased.Load() != 1 {
				t.Error("repeated cleanup released the successor twice")
			}
		})
	}
}
