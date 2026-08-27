package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/cognitive"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// The work item is "fire this alarm and settle its next state". When the
// settling fails the alarm row keeps its old deadline and fires again —
// so reporting the work DONE told the queue one thing while the clock
// did another, and the only trace was a log line.

type fakeTime struct {
	settleErr error
	invoked   int
	cleared   int
}

func (f *fakeTime) OwnerFor(string) (cognitive.AlarmOwner, bool) { return nil, true }
func (f *fakeTime) InvokeAlarmOwner(context.Context, cognitive.AlarmOwner, store.Alarm) cognitive.AlarmResult {
	f.invoked++
	return cognitive.AlarmResult{Accepted: true}
}
func (f *fakeTime) ApplyAlarmTransitions(store.Alarm, cognitive.AlarmResult) error {
	return f.settleErr
}
func (f *fakeTime) ClearPendingDispatch(string) { f.cleared++ }

const alarmPayload = `{"alarm_id":"alarm_dream","owner":"dream","clock":"wall","deadline":1,"payload":"{}"}`

func TestAFailedAlarmSettlingIsNotCompletedWork(t *testing.T) {
	ft := &fakeTime{settleErr: errors.New("database is locked")}
	h := &alarmHandler{time: ft}

	err := h.RunWork(context.Background(), &store.WorkItem{Kind: "alarm.dream", Payload: alarmPayload})
	if err == nil {
		t.Fatal("a failed alarm settling was reported as completed work — the executor marks it DONE " +
			"while the alarm still holds its old deadline")
	}
	if !strings.Contains(err.Error(), "database is locked") {
		t.Fatalf("the failure does not carry its cause: %v", err)
	}
	if ft.invoked != 1 {
		t.Fatalf("the owner ran %d times", ft.invoked)
	}
}

// The ordinary path still reports success, or every alarm would look
// broken.
func TestASettledAlarmIsCompletedWork(t *testing.T) {
	ft := &fakeTime{}
	h := &alarmHandler{time: ft}

	if err := h.RunWork(context.Background(), &store.WorkItem{Kind: "alarm.dream", Payload: alarmPayload}); err != nil {
		t.Fatalf("a normal alarm firing was reported as failed: %v", err)
	}
}

// WHAT THE FIRING IS, NOT JUST WHAT IT SAYS. repeat_every decides how
// TIME settles a firing, so an alarm that loses it across the queue
// arrives one-shot and settles wrong in BOTH directions — accepted
// deletes it, declined leaves it instantly due. It is carried, and this
// is the test that says so.

type captureQ struct{ item *store.WorkItem }

func (c *captureQ) EnqueueWork(i *store.WorkItem) (string, error)      { c.item = i; return "w1", nil }
func (c *captureQ) ClaimWork([]string, int64) (*store.WorkItem, error) { return nil, nil }
func (c *captureQ) CompleteWork(string) error                          { return nil }
func (c *captureQ) FailWork(string, string) error                      { return nil }
func (c *captureQ) SweepExpiredLeases(int64) (int, error)              { return 0, nil }
func (c *captureQ) PendingWorkCount() (int, error)                     { return 0, nil }

type settleCapture struct{ got store.Alarm }

func (s *settleCapture) OwnerFor(string) (cognitive.AlarmOwner, bool) { return nil, true }
func (s *settleCapture) InvokeAlarmOwner(context.Context, cognitive.AlarmOwner, store.Alarm) cognitive.AlarmResult {
	return cognitive.AlarmResult{Accepted: true}
}
func (s *settleCapture) ApplyAlarmTransitions(a store.Alarm, _ cognitive.AlarmResult) error {
	s.got = a
	return nil
}
func (s *settleCapture) ClearPendingDispatch(string) {}

func TestRepeatEverySurvivesTheWorkQueue(t *testing.T) {
	every := int64(600000)
	dispatched := store.Alarm{
		AlarmID: "rhythm", OwnerName: "rhythm", Clock: "wall",
		Deadline: 1787665428066, RepeatEvery: &every, Payload: "",
	}

	q := &captureQ{}
	// The adapter goes through the executor door now (the door that
	// pokes the loop); an unstarted executor over the capture queue is
	// the same seam one layer up.
	if err := (alarmEnqueuerAdapter{ex: cognitive.NewExecutor(q)}).EnqueueAlarm(dispatched); err != nil {
		t.Fatal(err)
	}

	cap := &settleCapture{}
	if err := (&alarmHandler{time: cap}).RunWork(context.Background(), q.item); err != nil {
		t.Fatal(err)
	}

	if cap.got.RepeatEvery == nil {
		t.Fatalf("repeat_every was lost across the queue (payload: %s) — the accepted pass would DELETE this alarm "+
			"and a declined pass would leave it instantly due", q.item.Payload)
	}
	if *cap.got.RepeatEvery != every {
		t.Fatalf("repeat_every changed across the queue: %d -> %d", every, *cap.got.RepeatEvery)
	}
}

// A one-shot alarm must still arrive one-shot: carrying the field must
// not invent a repeat where the row had none.
func TestAOneShotAlarmStaysOneShotAcrossTheWorkQueue(t *testing.T) {
	q := &captureQ{}
	if err := (alarmEnqueuerAdapter{ex: cognitive.NewExecutor(q)}).EnqueueAlarm(store.Alarm{
		AlarmID: "morning_brief", OwnerName: "morning_brief", Clock: "wall", Deadline: 42,
	}); err != nil {
		t.Fatal(err)
	}
	cap := &settleCapture{}
	if err := (&alarmHandler{time: cap}).RunWork(context.Background(), q.item); err != nil {
		t.Fatal(err)
	}
	if cap.got.RepeatEvery != nil {
		t.Fatalf("a one-shot alarm gained a repeat of %d across the queue", *cap.got.RepeatEvery)
	}
}
