package app

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// .
// .
// .
// .
// .
// .
func TestVoiceTurnHandoffKeepsSuccessorReplyAndFinishHold(t *testing.T) {
	for _, oldUnanswered := range []bool{false, true} {
		name := "previous turn already settled"
		if oldUnanswered {
			name = "previous turn lost its reply"
		}
		t.Run(name, func(t *testing.T) {
			a := &App{cfg: &Config{}, turnGate: make(chan struct{})}
			var oldReleased, nextReleased atomic.Int32
			if oldUnanswered {
				a.holdVoice(&voiceBinding{session: "previous", done: func() { oldReleased.Add(1) }})
			}
			h, engine := newTrackedSession(a, "vs-next", true)
			binding := a.voiceBindingFor(heardUtterance{SessionID: h.id, Gen: h.gen.Load()})
			giveBack := binding.done
			binding.done = func() { nextReleased.Add(1); giveBack() }
			releasing, released := make(chan struct{}), make(chan struct{})
			a.turnFgRelease = func() { close(releasing) }
			go func() { a.releaseTurn(); close(released) }()
			select {
			case <-releasing:
			case <-time.After(5 * time.Second):
				t.Fatal("previous turn never reached its handoff")
			}
			// .
			a.turnMu.Lock()
			select {
			case <-a.turnGate:
			case <-time.After(5 * time.Second):
				a.turnMu.Unlock()
				t.Fatal("previous turn never returned the token")
			}
			a.turnVoice = append(a.turnVoice, binding)
			a.turnMu.Unlock()
			select {
			case <-released:
			case <-time.After(5 * time.Second):
				t.Fatal("previous releaser never retired")
			}
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
			// .
			// .
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
