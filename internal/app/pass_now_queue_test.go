package app

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/cognitive"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// recordingEnqueuer stands where the work queue stands: it keeps what a
// dispatch enqueued, with the hold token the handler will carry back.
type recordingEnqueuer struct {
	alarms []store.Alarm
	tokens []uint64
}

func (r *recordingEnqueuer) EnqueueAlarm(a store.Alarm, token uint64) error {
	r.alarms = append(r.alarms, a)
	r.tokens = append(r.tokens, token)
	return nil
}

// gateOwner blocks its first pass until released and defers every pass, as
// the rhythm does when a turn holds; later passes return at once.
type gateOwner struct {
	name    string
	first   chan struct{}
	release chan struct{}
	calls   int
}

func (g *gateOwner) Name() string { return g.name }
func (g *gateOwner) OnAlarm(context.Context, string, string, int64, string) cognitive.AlarmResult {
	g.calls++
	if g.calls == 1 {
		close(g.first)
		<-g.release
	}
	return cognitive.AlarmResult{Deferred: true}
}

func queuedPayload(t *testing.T, a store.Alarm, token uint64) *store.WorkItem {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"alarm_id": a.AlarmID, "owner": a.OwnerName, "clock": a.Clock, "deadline": a.Deadline,
		"repeat_every": a.RepeatEvery, "payload": a.Payload, "hold_token": token,
	})
	if err != nil {
		t.Fatal(err)
	}
	return &store.WorkItem{Kind: "alarm." + a.OwnerName, Payload: string(payload)}
}

// A queued payload left by a predecessor TIME, delivered through the real
// handler after a successor TIME has reserved the same row, does not
// release the successor's hold: the press meets in_flight until the
// successor's own pass settles (a review, 2026-10-31 07:48Z item 1, 11:57Z item 1).
func TestAnOldQueuedPayloadDoesNotReleaseASuccessorsHold(t *testing.T) {
	st, err := store.NewForTest(filepath.Join(t.TempDir(), "queue.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	period := int64(3_600_000)
	ctx := context.Background()

	// The predecessor dispatches the due row into the queue and dies before
	// the handler runs: its payload carries its own token.
	tm1 := cognitive.NewTIME(st, st)
	tm1.RegisterOwner(&gateOwner{name: "rhythm", first: make(chan struct{}), release: make(chan struct{})})
	if err := st.SetAlarm("rhythm", "rhythm", "wall", cognitive.WallNow()-1000, &period, ""); err != nil {
		t.Fatal(err)
	}
	q1 := &recordingEnqueuer{}
	tm1.SetAlarmEnqueuer(q1)
	if err := tm1.EvaluateAll(ctx); err != nil {
		t.Fatal(err)
	}
	if len(q1.alarms) != 1 {
		t.Fatalf("the predecessor enqueued %d alarm(s), want 1", len(q1.alarms))
	}
	old := queuedPayload(t, q1.alarms[0], q1.tokens[0])

	// The successor reserves the same row with a pass in flight.
	tm2 := cognitive.NewTIME(st, st)
	live := &gateOwner{name: "rhythm", first: make(chan struct{}), release: make(chan struct{})}
	tm2.RegisterOwner(live)
	go tm2.EvaluateAll(ctx)
	<-live.first
	if res, _ := tm2.BringAlarmForward("rhythm"); res.Reason != cognitive.PressInFlight {
		t.Fatalf("with the successor's pass in flight the press must meet in_flight, not %+v", res)
	}

	// The old payload is delivered through the real handler against the
	// successor; its stale token must not release the live hold.
	h := &alarmHandler{time: tm2}
	_ = h.RunWork(ctx, old) // the owner's later pass returns at once
	if res, _ := tm2.BringAlarmForward("rhythm"); res.Reason != cognitive.PressInFlight {
		t.Fatalf("an old queued payload released the successor's hold: the press answered %+v", res)
	}

	// The successor's own settlement releases it.
	close(live.release)
	deadline := time.Now().Add(2 * time.Second)
	for {
		res, _ := tm2.BringAlarmForward("rhythm")
		if res.Reason != cognitive.PressInFlight {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the successor's own pass did not release its hold")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// With the work queue in place, a dispatch claims the row before it
// enqueues; a press while the item waits is in_flight, and the handler's
// settlement with the dispatch's own token releases it (a review, 2026-10-31
// 11:57Z item 2).
func TestAQueuedDispatchHoldsTheRowUntilItsHandlerSettles(t *testing.T) {
	st, err := store.NewForTest(filepath.Join(t.TempDir(), "queue2.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	period := int64(3_600_000)
	ctx := context.Background()
	tm := cognitive.NewTIME(st, st)
	tm.RegisterOwner(&gateOwner{name: "rhythm", first: make(chan struct{}), release: make(chan struct{})})
	if err := st.SetAlarm("rhythm", "rhythm", "wall", cognitive.WallNow()-1000, &period, ""); err != nil {
		t.Fatal(err)
	}
	q := &recordingEnqueuer{}
	tm.SetAlarmEnqueuer(q)
	if err := tm.EvaluateAll(ctx); err != nil {
		t.Fatal(err)
	}
	if len(q.alarms) != 1 {
		t.Fatalf("enqueued %d alarm(s), want 1", len(q.alarms))
	}
	if res, _ := tm.BringAlarmForward("rhythm"); res.Reason != cognitive.PressInFlight {
		t.Fatalf("while the item waits in the queue the press must meet in_flight, not %+v", res)
	}
	if err := tm.ApplyAlarmTransitions(q.alarms[0], cognitive.AlarmResult{Deferred: true}, q.tokens[0]); err != nil {
		t.Fatal(err)
	}
	res, err := tm.BringAlarmForward("rhythm")
	if err != nil {
		t.Fatal(err)
	}
	if res.Reason == cognitive.PressInFlight {
		t.Fatalf("after the handler settled with the dispatch's token the hold must be released: %+v", res)
	}
}

// The original arm of the stale-settlement carry, through the real handler:
// a predecessor's queued payload is delivered after the row has MOVED under
// a successor's live hold. Its settlement CASes against the deadline it
// carried and is refused; the row keeps the moved deadline and the
// successor's hold stays; the successor's own settlement then moves the
// row on and releases (host 2026-10-31 12:12Z; a review 15:38Z).
func TestAnOldQueuedPayloadAfterTheRowMovedIsRefusedAndTheSuccessorSettles(t *testing.T) {
	st, err := store.NewForTest(filepath.Join(t.TempDir(), "queue3.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	period := int64(3_600_000)
	ctx := context.Background()

	// The predecessor dispatches the row at its first deadline and dies.
	first := cognitive.WallNow() - 1_000
	tm1 := cognitive.NewTIME(st, st)
	tm1.RegisterOwner(&gateOwner{name: "rhythm", first: make(chan struct{}), release: make(chan struct{})})
	if err := st.SetAlarm("rhythm", "rhythm", "wall", first, &period, ""); err != nil {
		t.Fatal(err)
	}
	q1 := &recordingEnqueuer{}
	tm1.SetAlarmEnqueuer(q1)
	if err := tm1.EvaluateAll(ctx); err != nil {
		t.Fatal(err)
	}
	if len(q1.alarms) != 1 || q1.alarms[0].Deadline != first {
		t.Fatalf("the predecessor enqueued %+v, want one alarm at %d", q1.alarms, first)
	}
	old := queuedPayload(t, q1.alarms[0], q1.tokens[0])

	// The row moves to a second deadline, still due, and a successor
	// dispatches it with a pass in flight.
	moved := first + 500
	if ok, err := st.UpdateAlarmDeadlineCAS("rhythm", first, moved); err != nil || !ok {
		t.Fatalf("move the row: ok=%v err=%v", ok, err)
	}
	tm2 := cognitive.NewTIME(st, st)
	live := &gateOwner{name: "rhythm", first: make(chan struct{}), release: make(chan struct{})}
	tm2.RegisterOwner(live)
	go tm2.EvaluateAll(ctx)
	<-live.first

	// The old payload arrives through the real handler.
	h := &alarmHandler{time: tm2}
	if err := h.RunWork(ctx, old); err != nil {
		t.Fatalf("a stale settlement is not an error, yet: %v", err)
	}
	row, found, err := st.AlarmByID("rhythm")
	if err != nil || !found {
		t.Fatalf("read the row: found=%v err=%v", found, err)
	}
	if row.Deadline != moved {
		t.Fatalf("the old payload's settlement moved the row to %d; its CAS against %d must be refused and leave %d", row.Deadline, first, moved)
	}
	if res, _ := tm2.BringAlarmForward("rhythm"); res.Reason != cognitive.PressInFlight {
		t.Fatalf("the old payload released the successor's hold: the press answered %+v", res)
	}

	// The successor's own settlement moves the row on and releases.
	close(live.release)
	deadline := time.Now().Add(2 * time.Second)
	for {
		res, _ := tm2.BringAlarmForward("rhythm")
		if res.Reason != cognitive.PressInFlight {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the successor's own pass did not release its hold")
		}
		time.Sleep(10 * time.Millisecond)
	}
	row, _, _ = st.AlarmByID("rhythm")
	if row.Deadline <= moved {
		t.Fatalf("the successor's settlement left the row at %d; it must move on from %d", row.Deadline, moved)
	}
}
