package store

import (
	"strings"
	"testing"
)

// .
// .
// .
// .
// .

func TestCancellingATimerThatDoesNotExistIsNotASuccess(t *testing.T) {
	s := testStore(t)
	err := s.CancelAlarm("timers", "alarm_never_set")
	if err == nil {
		t.Fatal("cancelling a nonexistent alarm reported success — the identity would say \"cancelled\"")
	}
	if !strings.Contains(err.Error(), "no alarm") {
		t.Fatalf("the refusal does not say the timer is not there: %v", err)
	}
}

// .
// .
func TestCancellingSomeoneElsesAlarmSaysSo(t *testing.T) {
	s := testStore(t)
	if err := s.SetAlarm("alarm_dream", "dream", "wall", 1<<40, nil, "{}"); err != nil {
		t.Fatal(err)
	}
	err := s.CancelAlarm("timers", "alarm_dream")
	if err == nil {
		t.Fatal("cancelling another owner's alarm reported success")
	}
	if !strings.Contains(err.Error(), "dream") {
		t.Fatalf("the refusal does not name the real owner: %v", err)
	}
	// .
	due, derr := s.DueAlarms("wall", 1<<62, 10)
	if derr != nil {
		t.Fatal(derr)
	}
	if len(due) != 1 {
		t.Fatalf("the other owner's alarm was removed anyway: %+v", due)
	}
}

// .
// .
func TestCancellingYourOwnAlarmWorks(t *testing.T) {
	s := testStore(t)
	if err := s.SetAlarm("alarm_mine", "timers", "wall", 1<<40, nil, "{}"); err != nil {
		t.Fatal(err)
	}
	if err := s.CancelAlarm("timers", "alarm_mine"); err != nil {
		t.Fatalf("cancelling an owned alarm was refused: %v", err)
	}
	due, _ := s.DueAlarms("wall", 1<<62, 10)
	if len(due) != 0 {
		t.Fatalf("the alarm survived its own cancellation: %+v", due)
	}
	// .
	// .
	if err := s.CancelAlarm("timers", "alarm_mine"); err == nil {
		t.Fatal("cancelling the same alarm twice reported success both times")
	}
}
