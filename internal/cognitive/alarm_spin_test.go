package cognitive

import (
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/store"
)

// .
// .
// .
// .
// .
// .
// .
// .

func liveAlarm(t *testing.T, st *store.Store, id string) (store.Alarm, bool) {
	t.Helper()
	as, err := st.DueAlarms("wall", 1<<62, 50)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range as {
		if a.AlarmID == id {
			return a, true
		}
	}
	return store.Alarm{}, false
}

// .
// .
// .
func TestAnAcceptedRepeatingAlarmReArmsRatherThanBeingDeleted(t *testing.T) {
	tm, st := newTIME(t)
	every := int64(600000)
	if err := st.SetAlarm("rhythm", "rhythm", "wall", 1000, &every, ""); err != nil {
		t.Fatal(err)
	}
	fired, _ := liveAlarm(t, st, "rhythm")

	if err := tm.ApplyAlarmTransitions(fired, AlarmResult{Accepted: true}); err != nil {
		t.Fatal(err)
	}
	a, ok := liveAlarm(t, st, "rhythm")
	if !ok {
		t.Fatal("an accepted repeating alarm was DELETED — metabolism stops permanently after one successful pass")
	}
	if a.Deadline <= 1000 {
		t.Fatalf("accepted but not re-armed: deadline still %d", a.Deadline)
	}
}

// .
// .
// .
func TestADeclinedRepeatingAlarmDoesNotStayInstantlyDue(t *testing.T) {
	tm, st := newTIME(t)
	every := int64(600000)
	if err := st.SetAlarm("rhythm", "rhythm", "wall", 1000, &every, ""); err != nil {
		t.Fatal(err)
	}
	fired, _ := liveAlarm(t, st, "rhythm")

	if err := tm.ApplyAlarmTransitions(fired, AlarmResult{}); err != nil {
		t.Fatal(err)
	}
	a, ok := liveAlarm(t, st, "rhythm")
	if !ok {
		t.Fatal("a declined alarm was deleted")
	}
	if a.Deadline <= WallNow() {
		t.Fatalf("declined alarm left at %d with now=%d — due again immediately, which is the hot loop", a.Deadline, WallNow())
	}
}

// .
// .
// .
func TestADeclinedPastDueOneShotIsDeferredNotSpun(t *testing.T) {
	tm, st := newTIME(t)
	if err := st.SetAlarm("brief", "morning_brief", "wall", 1000, nil, ""); err != nil {
		t.Fatal(err)
	}
	fired, _ := liveAlarm(t, st, "brief")

	if err := tm.ApplyAlarmTransitions(fired, AlarmResult{}); err != nil {
		t.Fatal(err)
	}
	a, ok := liveAlarm(t, st, "brief")
	if !ok {
		t.Fatal("a declined one-shot was deleted")
	}
	if a.Deadline <= WallNow() {
		t.Fatalf("declined one-shot left at %d with now=%d — due again immediately", a.Deadline, WallNow())
	}
	if a.RepeatEvery != nil {
		t.Fatal("deferring a one-shot must not give it a repeat")
	}
}

// .
// .
// .
func TestADeclinedFutureAlarmKeepsItsDeadlineExactly(t *testing.T) {
	tm, st := newTIME(t)
	future := WallNow() + 3600_000
	if err := st.SetAlarm("brief", "morning_brief", "wall", future, nil, ""); err != nil {
		t.Fatal(err)
	}
	fired, _ := liveAlarm(t, st, "brief")

	if err := tm.ApplyAlarmTransitions(fired, AlarmResult{}); err != nil {
		t.Fatal(err)
	}
	a, ok := liveAlarm(t, st, "brief")
	if !ok {
		t.Fatal("a declined future alarm was deleted")
	}
	if a.Deadline != future {
		t.Fatalf("a declined future alarm was moved from %d to %d — its deadline is a promise", future, a.Deadline)
	}
}
