package app

import (
	"math"
	"path/filepath"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/cognitive"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// maintenance_arming_test.go — the daily pass has to hold its absolute
// hour twice over, because two different clocks can push it: arming runs
// on EVERY boot, and every firing sets the next one. A deadline measured
// from boot is pushed forward by the next boot, so a host restarted more
// often than the delay never reaches it. A deadline measured from the end
// of the pass slides later by however long that pass took, every day,
// forever. Both endings are 04:00 becoming never, so both are tested.

// armingFixture: a real store and a real TIME, never started — SetAlarm's
// gate is owner registration, and nothing here should dispatch.
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

// armedAlarm reads the stored row through the same reader TIME dispatches
// from — repeat_every included, since that field alone decides how a
// firing settles. Absent is a real answer, not a fixture failure: an
// accepted firing that names no next deadline DELETES the row.
func armedAlarm(t *testing.T, st *store.Store) (store.Alarm, bool) {
	t.Helper()
	rows, err := st.DueAlarms("wall", math.MaxInt64, 10) // every wall alarm, due or not
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

// armedDeadline is the deadline the machine will actually wake on, not
// what the caller believes it asked for.
func armedDeadline(t *testing.T, st *store.Store) int64 {
	t.Helper()
	a, ok := armedAlarm(t, st)
	if !ok {
		t.Fatal("no maintenance alarm armed")
	}
	return a.Deadline
}

// TWO BOOTS, ONE DEADLINE. The second boot must leave the stored deadline
// exactly where the first put it; a deadline that walks forward with the
// boot is how a daemon restarting ~18 times a day ran the pass zero times.
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

	// And it is the absolute hour, not an offset from either boot.
	want := time.Date(2026, 8, 28, maintenanceHourLocal, 0, 0, 0, time.Local).UnixMilli()
	if first != want {
		t.Fatalf("armed %s, want the next %02d:00 local (%s)",
			time.UnixMilli(first), maintenanceHourLocal, time.UnixMilli(want))
	}
}

// IDEMPOTENT MUST NOT MEAN SILENT. A first-ever boot (no row) and a boot
// after the machine was off through the deadline (a row long past) must
// both leave a FUTURE deadline standing, so the daily pass keeps a next
// firing to reach.
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

	// Two days overdue: TIME's boot catch-up fires that row, and arming
	// must leave the NEXT deadline standing rather than a past one.
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

// A FIRING MUST LEAVE THE NEXT ONE STANDING, AT THE HOUR. This is the
// half no boot test can see. TIME settles a repeating alarm at the clock
// read AFTER the owner returned, so a repeat_every re-arms at
// finished-at+24h and the pass slides later by its own duration every
// day; a one-shot that accepts without naming a next deadline is deleted
// outright. So the real settle path runs here — the real owner, the real
// pass over a real signed chain, the real CAS against the stored row.
func TestAFiringRearmsTheNextAbsoluteDailyHour(t *testing.T) {
	a, _, _ := maintApp(t)
	tf := cognitive.NewTIME(a.store, a.store)
	tf.RegisterOwner(maintenanceOwner{a})

	// Armed the way a boot arms it, a day back, so the row is really DUE:
	// a firing is TIME dispatching a deadline that has passed. Arming owns
	// the row's shape — its repeat above all — and the settle reads both.
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
	// A day, plus the hour a DST fall-back can add to it.
	if !at.After(fired) || at.Sub(fired) > 25*time.Hour {
		t.Fatalf("the firing re-armed at %s, %v from the firing — that is not tomorrow's pass",
			at, at.Sub(fired))
	}
}
