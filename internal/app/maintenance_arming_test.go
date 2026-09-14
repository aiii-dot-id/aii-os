package app

import (
	"math"
	"path/filepath"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/cognitive"
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
// .
func armingFixture(t *testing.T) (*cognitive.TIME, *store.Store) {
	t.Helper()
	st, err := store.New(filepath.Join(t.TempDir(), "arming.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	tf := cognitive.NewTIME(st, st)
	tf.RegisterOwner(maintenanceOwner{})
	return tf, st
}

// .
// .
// .
// .
func armedAlarm(t *testing.T, st *store.Store) (store.Alarm, bool) {
	t.Helper()
	rows, err := st.DueAlarms("wall", math.MaxInt64, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range rows {
		if a.AlarmID == maintenanceAlarmID {
			return a, true
		}
	}
	return store.Alarm{}, false
}

// .
// .
func armedDeadline(t *testing.T, st *store.Store) int64 {
	t.Helper()
	a, ok := armedAlarm(t, st)
	if !ok {
		t.Fatal("no maintenance alarm armed")
	}
	return a.Deadline
}

// .
// .
// .
func TestSecondBootDoesNotMoveTheMaintenanceDeadline(t *testing.T) {
	tf, st := armingFixture(t)

	firstBoot := time.Date(2026, 8, 27, 9, 0, 0, 0, time.Local)
	if err := armMaintenanceAlarm(tf, firstBoot); err != nil {
		t.Fatal(err)
	}
	first := armedDeadline(t, st)

	secondBoot := firstBoot.Add(40 * time.Minute)
	if err := armMaintenanceAlarm(tf, secondBoot); err != nil {
		t.Fatal(err)
	}
	if second := armedDeadline(t, st); second != first {
		t.Fatalf("the second boot moved the deadline by %v (%d → %d) — a host rebooting faster than that never reaches it",
			time.Duration(second-first)*time.Millisecond, first, second)
	}

	// .
	want := time.Date(2026, 8, 28, maintenanceHourLocal, 0, 0, 0, time.Local).UnixMilli()
	if first != want {
		t.Fatalf("armed %s, want the next %02d:00 local (%s)",
			time.UnixMilli(first), maintenanceHourLocal, time.UnixMilli(want))
	}
}

// .
// .
// .
// .
func TestArmingLeavesAFutureDeadlineWhenAbsentOrOverdue(t *testing.T) {
	tf, st := armingFixture(t)

	boot := time.Date(2026, 8, 27, 5, 0, 0, 0, time.Local)
	if err := armMaintenanceAlarm(tf, boot); err != nil {
		t.Fatal(err)
	}
	if got := armedDeadline(t, st); got <= boot.UnixMilli() {
		t.Fatalf("first boot armed %s, which is not after the boot at %s — nothing would ever fire",
			time.UnixMilli(got), boot)
	}

	// .
	// .
	overdue := boot.AddDate(0, 0, -2).UnixMilli()
	if err := tf.SetAlarm(maintenanceAlarmID, maintenanceOwnerName, "wall", overdue, nil, ""); err != nil {
		t.Fatal(err)
	}
	later := boot.Add(3 * time.Hour)
	if err := armMaintenanceAlarm(tf, later); err != nil {
		t.Fatal(err)
	}
	if got := armedDeadline(t, st); got <= later.UnixMilli() {
		t.Fatalf("after catch-up the deadline is still %s (now %s) — the pass has no next firing",
			time.UnixMilli(got), later)
	}
}

// .
// .
// .
// .
// .
// .
// .
func TestAFiringRearmsTheNextAbsoluteDailyHour(t *testing.T) {
	a, _, _ := maintApp(t)
	tf := cognitive.NewTIME(a.store, a.store)
	tf.RegisterOwner(maintenanceOwner{a})

	// .
	// .
	// .
	if err := armMaintenanceAlarm(tf, time.Now().AddDate(0, 0, -1)); err != nil {
		t.Fatal(err)
	}
	due, ok := armedAlarm(t, a.store)
	if !ok {
		t.Fatal("nothing armed to fire")
	}
	owner, ok := tf.OwnerFor(maintenanceOwnerName)
	if !ok {
		t.Fatal("the maintenance owner is not registered")
	}

	fired := time.Now()
	if err := tf.ApplyAlarmTransitions(due, tf.InvokeAlarmOwner(t.Context(), owner, due)); err != nil {
		t.Fatal(err)
	}

	next, ok := armedAlarm(t, a.store)
	if !ok {
		t.Fatal("the firing DELETED the alarm — a pass that accepts without naming its next deadline " +
			"runs once in the life of the machine and leaves no row to say it stopped")
	}
	at := time.UnixMilli(next.Deadline)
	if at.Hour() != maintenanceHourLocal || at.Minute() != 0 || at.Second() != 0 {
		t.Fatalf("the firing re-armed at %s, not the next %02d:00 local — a deadline measured from the end "+
			"of the pass slides later by every pass's own duration until the hour means nothing",
			at, maintenanceHourLocal)
	}
	// .
	if !at.After(fired) || at.Sub(fired) > 25*time.Hour {
		t.Fatalf("the firing re-armed at %s, %v from the firing — that is not tomorrow's pass",
			at, at.Sub(fired))
	}
}
