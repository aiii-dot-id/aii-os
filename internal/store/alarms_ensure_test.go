package store

import (
	"math"
	"strings"
	"testing"
)

func TestEnsureAlarmPreservesDeadlineAndRepairsIncompatibleShape(t *testing.T) {
	s := testStore(t)
	const id = "reflect:self_model"
	oldCadence, currentCadence := int64(25), int64(50)
	read := func(clock string) (Alarm, bool) {
		t.Helper()
		rows, err := s.DueAlarms(clock, math.MaxInt64, 10)
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range rows {
			if row.AlarmID == id {
				return row, true
			}
		}
		return Alarm{}, false
	}
	if err := s.EnsureAlarm(id, "rhythm", "life", 50, &currentCadence, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.EnsureAlarm(id, "rhythm", "life", 80, &currentCadence, ""); err != nil {
		t.Fatal(err)
	}
	if got, ok := read("life"); !ok || got.Deadline != 50 {
		t.Fatalf("second boot moved a matching deadline: %+v %v", got, ok)
	}
	if err := s.SetAlarm(id, "rhythm", "wall", 123, &oldCadence, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.EnsureAlarm(id, "rhythm", "life", 100, &currentCadence, ""); err != nil {
		t.Fatal(err)
	}
	got, ok := read("life")
	if !ok || got.Deadline != 100 || got.RepeatEvery == nil || *got.RepeatEvery != currentCadence {
		t.Fatalf("old clock/cadence was not repaired: %+v %v", got, ok)
	}
	if _, old := read("wall"); old {
		t.Fatal("incompatible wall alarm stayed armed")
	}
	if err := s.SetAlarm(id, "rhythm", "life", 125, &oldCadence, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.EnsureAlarm(id, "rhythm", "life", 150, &currentCadence, ""); err != nil {
		t.Fatal(err)
	}
	if got, ok := read("life"); !ok || got.Deadline != 125 || got.RepeatEvery == nil || *got.RepeatEvery != currentCadence {
		t.Fatalf("new cadence postponed an owed alarm or failed to update the repeat: %+v %v", got, ok)
	}
}

func TestEnsureAlarmDoesNotTakeAnotherOwnersAlarm(t *testing.T) {
	s := testStore(t)
	const id = "reflect:self_model"
	period := int64(50)
	if err := s.SetAlarm(id, "another", "life", 40, &period, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.EnsureAlarm(id, "rhythm", "life", 50, &period, ""); err == nil || !strings.Contains(err.Error(), "owner mismatch") {
		t.Fatalf("cross-owner alarm was silently adopted: %v", err)
	}
	rows, err := s.DueAlarms("life", math.MaxInt64, 10)
	if err != nil || len(rows) != 1 || rows[0].OwnerName != "another" || rows[0].Deadline != 40 {
		t.Fatalf("other owner's alarm changed: %+v %v", rows, err)
	}
}
