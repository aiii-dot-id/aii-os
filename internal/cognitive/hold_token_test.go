package cognitive

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/store"
)

// A hold token names the TIME that took it: a queued payload from a
// predecessor TIME, released after a successor has reserved the same row,
// leaves the successor's hold in place (a review, 2026-10-31 07:48Z item 1).
func TestAStalePayloadCannotReleaseASuccessorsHold(t *testing.T) {
	tm1, st := newTIME(t)
	tm1.mu.Lock()
	stale, ok := tm1.acquireHoldLocked("x")
	tm1.mu.Unlock()
	if !ok {
		t.Fatal("the first TIME could not reserve x")
	}

	tm2 := NewTIME(st, st)
	tm2.mu.Lock()
	live, ok := tm2.acquireHoldLocked("x")
	tm2.mu.Unlock()
	if !ok {
		t.Fatal("the second TIME could not reserve x")
	}
	if stale == live {
		t.Fatalf("two TIMEs drew the same first token %d: the token does not name its TIME", stale)
	}

	tm2.releaseHold("x", stale) // the predecessor's queued payload settles late
	tm2.mu.Lock()
	_, stillHeld := tm2.pendingDispatch["x"]
	tm2.mu.Unlock()
	if !stillHeld {
		t.Fatal("a predecessor's token released the successor's hold")
	}
	tm2.releaseHold("x", live)
	tm2.mu.Lock()
	_, stillHeld = tm2.pendingDispatch["x"]
	tm2.mu.Unlock()
	if stillHeld {
		t.Fatal("the owner's own token did not release its hold")
	}
}

// Check and claim are one locked act: a dispatch paused between its check
// and its write still holds the lock, so a press arriving then waits and
// is refused in_flight once the dispatch has claimed; neither side's claim
// is overwritten (a review, 2026-10-31 06:29Z item 1 and 07:48Z item 2).
func TestADispatchAndAPressCannotBothClaimOneRow(t *testing.T) {
	tm, st := newTIME(t)
	period := int64(3_600_000)
	if err := st.SetAlarm("held", "held", "wall", WallNow()+period, &period, ""); err != nil {
		t.Fatal(err)
	}
	ownerMayFinish := make(chan struct{})
	tm.RegisterOwner(&waitingOwner{name: "held", wait: ownerMayFinish})

	paused := make(chan struct{})
	resume := make(chan struct{})
	var once sync.Once
	tm.holdHook = func() {
		once.Do(func() {
			close(paused)
			<-resume
		})
	}

	row, ok, err := st.AlarmByID("held")
	if err != nil || !ok {
		t.Fatalf("read the row: ok=%v err=%v", ok, err)
	}
	dispatched := make(chan struct{})
	go func() {
		tm.dispatchAlarm(context.Background(), row) // claims; pauses in the hook under the lock
		close(dispatched)
	}()
	<-paused

	pressDone := make(chan BringForwardResult, 1)
	go func() {
		res, err := tm.BringAlarmForward("held")
		if err != nil {
			t.Error(err)
		}
		pressDone <- res
	}()
	// The press is now on its way to t.mu, which the paused dispatch holds;
	// a bounded wait lets it arrive at the lock before the dispatch resumes.
	time.Sleep(100 * time.Millisecond)
	close(resume)
	// The dispatch has written its hold and is now inside the owner, which
	// waits; the press takes the lock, meets the hold, and answers.
	res := <-pressDone
	close(ownerMayFinish)
	<-dispatched

	if res.Moved || res.Reason != PressInFlight {
		t.Fatalf("a press that met a dispatch between its check and its claim must be refused in_flight, not %+v", res)
	}
	tm.mu.Lock()
	holders := len(tm.pendingDispatch)
	tm.mu.Unlock()
	if holders != 0 {
		t.Fatalf("after the held dispatch settled, %d hold(s) remain: a claim was overwritten rather than refused", holders)
	}
}

// waitingOwner holds its pass open until told to finish, so a hold stays
// in flight for exactly as long as a test needs; it then defers, as the
// rhythm does when a turn holds.
type waitingOwner struct {
	name string
	wait <-chan struct{}
}

func (w *waitingOwner) Name() string { return w.name }
func (w *waitingOwner) OnAlarm(context.Context, string, string, int64, string) AlarmResult {
	<-w.wait
	return AlarmResult{Deferred: true}
}

// A predecessor TIME's queued payload settles against the deadline it
// carried. When the row has moved on and a successor holds it for its own
// dispatch, that settlement's CAS is refused, the successor's hold stays,
// and the successor's own settlement rearms the row and releases the hold
// (the stale-CAS settlement carried from f686a552, host 2026-10-31 12:12Z).
func TestAStalePayloadsSettlementIsRefusedAndTheSuccessorsRearms(t *testing.T) {
	tm1, st := newTIME(t)
	period := int64(3_600_000)
	first := WallNow() - 1_000 // the deadline the predecessor's payload carried
	if err := st.SetAlarm("x", "o", "wall", first, &period, ""); err != nil {
		t.Fatal(err)
	}
	tm1.mu.Lock()
	stale, ok := tm1.acquireHoldLocked("x")
	tm1.mu.Unlock()
	if !ok {
		t.Fatal("the first TIME could not reserve x")
	}

	// The row moves on under the first TIME's hold, and a successor TIME
	// reserves it for the dispatch of the moved deadline.
	moved := WallNow() + 5_000
	if ok, err := st.UpdateAlarmDeadlineCAS("x", first, moved); err != nil || !ok {
		t.Fatalf("move the row: ok=%v err=%v", ok, err)
	}
	tm2 := NewTIME(st, st)
	tm2.mu.Lock()
	live, ok := tm2.acquireHoldLocked("x")
	tm2.mu.Unlock()
	if !ok {
		t.Fatal("the second TIME could not reserve x")
	}

	// The predecessor's payload settles late, accepted, against the
	// deadline it carried.
	old := store.Alarm{AlarmID: "x", OwnerName: "o", Clock: "wall", Deadline: first, RepeatEvery: &period}
	if err := tm2.ApplyAlarmTransitions(old, AlarmResult{Accepted: true}, stale); err != nil {
		t.Fatalf("a stale settlement is not an error, yet: %v", err)
	}
	row, found, err := st.AlarmByID("x")
	if err != nil || !found {
		t.Fatalf("read the row: found=%v err=%v", found, err)
	}
	if row.Deadline != moved {
		t.Fatalf("the stale payload's CAS moved the row to %d; it must be refused and leave %d", row.Deadline, moved)
	}
	tm2.mu.Lock()
	_, held := tm2.pendingDispatch["x"]
	tm2.mu.Unlock()
	if !held {
		t.Fatal("the stale settlement released the successor's hold")
	}

	// The successor's own settlement rearms a period on and releases.
	before := WallNow()
	current := store.Alarm{AlarmID: "x", OwnerName: "o", Clock: "wall", Deadline: moved, RepeatEvery: &period}
	if err := tm2.ApplyAlarmTransitions(current, AlarmResult{Accepted: true}, live); err != nil {
		t.Fatal(err)
	}
	row, _, _ = st.AlarmByID("x")
	if row.Deadline < before+period {
		t.Fatalf("the successor's settlement did not rearm the row: deadline %d, want at least %d", row.Deadline, before+period)
	}
	tm2.mu.Lock()
	_, held = tm2.pendingDispatch["x"]
	tm2.mu.Unlock()
	if held {
		t.Fatal("the successor's own settlement did not release its hold")
	}
}
