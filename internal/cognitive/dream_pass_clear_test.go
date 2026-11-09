package cognitive

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/store"
)

// The reason a hold carries is the pass's own. It is cleared as every pass
// starts, so a pass that stalls for another cause after a no-room pass
// reads as a stall, not as the budget: the operator pressing again would
// be told to make room when room was not the trouble (F-COG-110; the
// pass-level harness carried from control 02 at 46df553e).
//
// Through the real door, OnAlarm: pass one finds no room because the model
// admits nothing; room is then made and pass two reaches the model, whose
// call fails. Without the clear at the pass's start, the first pass's
// reason outlives it and Held names the budget for a stall of the call.
func TestAPassClearsTheReasonTheLastOneLeft(t *testing.T) {
	st := &mockStore{unprocessedCnt: 1, experiences: []store.Experience{
		{ID: "e1", Content: "NOTED-e1, a thing that happened", Raw: 1},
	}}
	admits := false
	model := &admitsLLM{mockLLM: &mockLLM{}, admits: func(string) bool { return admits }}
	d := NewDream(st, model, &mockLedger{st: st}, &mockRingWriter{}, DreamConfig{Threshold: 1, Spacing: 10 * time.Minute})
	at := time.Date(2026, 10, 31, 11, 0, 0, 0, time.UTC)
	now := at
	d.now = func() time.Time { return now }
	ctx := context.Background()

	// Pass one: nothing fits, nothing is sent, and the hold names the budget.
	d.OnAlarm(ctx, "rhythm", "wall", now.UnixMilli(), "")
	if model.calls != 0 {
		t.Fatalf("a pass that found no room sent %d request(s); it must send none", model.calls)
	}
	if got := d.Held(); got != PressBudget {
		t.Fatalf("after a no-room pass the hold must read %q, not %q", PressBudget, got)
	}

	// Room is made; the next pass reaches the model and the call fails.
	admits = true
	model.err = errors.New("the model is unreachable")
	now = at.Add(11 * time.Minute) // past the spacing: the alarm runs a pass
	d.OnAlarm(ctx, "rhythm", "wall", now.UnixMilli(), "")
	if model.calls != 1 {
		t.Fatalf("the second pass must reach the model once, not %d time(s): the stall under test is the call's, not the room's", model.calls)
	}
	if got := d.Held(); got != PressStall {
		t.Fatalf("a stall whose cause the pass did not find must read %q, not %q: the reason the last pass left outlived it", PressStall, got)
	}
}
