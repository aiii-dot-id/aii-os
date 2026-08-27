package store

import (
	"strings"
	"testing"
)

// The identity says `timer cancel id=X` and is told "Timer X cancelled."
// A DELETE that deleted nothing used to produce that same sentence — for
// a timer that never existed, or one belonging to someone else — and the
// timer it meant to stop went on to fire. Same shape as "Sent to peer.":
// a report of an act that did not happen.

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

// "You have no such timer" and "that one is not yours" send the identity
// somewhere different, so they are different answers.
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
	// And it is still there — a refused cancel cancels nothing.
	due, derr := s.DueAlarms("wall", 1<<62, 10)
	if derr != nil {
		t.Fatal(derr)
	}
	if len(due) != 1 {
		t.Fatalf("the other owner's alarm was removed anyway: %+v", due)
	}
}

// The real cancellation still works, or the guard would be worse than
// the bug.
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
	// Cancelling it twice is now an honest "no such alarm", not a
	// second success.
	if err := s.CancelAlarm("timers", "alarm_mine"); err == nil {
		t.Fatal("cancelling the same alarm twice reported success both times")
	}
}
