package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/memory"
)

// cutover parks a memory behind a call in flight, changes what is
// admitted while it waits, and lets the call in flight end: the memory
// that waited must end with the refusal of kind, the call in flight must
// complete, and nothing more may reach the child.
func cutover(t *testing.T, change func(a *App), kind string) {
	t.Helper()
	a := liveApp(t)
	s, child := childFor(t, a)
	source := pluginSource{a: a, ap: s.ap, cue: time.Minute, memory: time.Minute}
	ctx := context.Background()

	release := child.hold("a memory in flight")
	defer release()
	first := asking(ctx, source, memory.KindMemory, "a memory in flight")
	if got := next(t, child.sent); got != "memory: a memory in flight" {
		t.Fatalf("first sent: %q", got)
	}
	waited := asking(ctx, source, memory.KindMemory, "a memory that waited")
	parked(t, 1)
	change(a)
	release()
	r := replied(t, waited, 20*time.Second)
	var off *meaningOff
	if !errors.As(r.err, &off) || off.Kind != kind {
		t.Fatalf("a memory that waited while the source was let go of must be refused (%s): %v", kind, r.err)
	}
	if m := replied(t, first, 20*time.Second); m.err != nil || len(m.vectors) != 1 {
		t.Fatalf("the call already sent must complete: %v", m.err)
	}
	// Both callers have answered: whatever either sent is on the channel.
	select {
	case got := <-child.sent:
		t.Fatalf("%q was sent to a source no longer admitted", got)
	default:
	}
}

func TestAMemoryThatWaitedIsNotSentToASourceTheOperatorNoLongerChooses(t *testing.T) {
	cutover(t, func(a *App) { setChoice(a, meaningChoiceNone) }, meaningOffChanged)
}

func TestAMemoryThatWaitedIsNotSentOnceSAFEBegins(t *testing.T) {
	cutover(t, func(a *App) {
		a.mode.mu.Lock()
		a.mode.mode, a.mode.reason = ModeSafe, "a test of SAFE"
		a.mode.mu.Unlock()
	}, meaningOffSafe)
}
