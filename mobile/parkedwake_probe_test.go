package mobile

// External-review probes (2026-08-20): the platform slot's behavior in
// the states the wake tests never visited — parked, cancelled, stopped.
// The slot is a shell callback, not a process timer, so quiesce parking
// must NOT starve it: an identity that sets an alarm while backgrounded
// still needs the OS to know.

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/cognitive"
	"github.com/aiii-dot-id/aii-os/internal/quiesce"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

func newProbeTIME(t *testing.T) (*cognitive.TIME, *fakeScheduler, *quiesce.Gate) {
	t.Helper()
	st, err := store.New(filepath.Join(t.TempDir(), "probe.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	tm := cognitive.NewTIME(st, st)
	f := &fakeScheduler{}
	tm.SetPlatformWake(wakeAdapter{s: f})
	gate := quiesce.NewGate()
	tm.SetQuiesceGate(gate)
	tm.RegisterOwner(acceptOwner{name: "probe"})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	t.Cleanup(tm.Stop)
	tm.Start(ctx)
	return tm, f, gate
}

func waitSchedulerSees(t *testing.T, f *fakeScheduler, step, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for f.last() != want {
		if time.Now().After(deadline) {
			t.Fatalf("%s: scheduler never saw %q (last %q)", step, want, f.last())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// An alarm created while BACKGROUNDED must still reach the OS slot:
// arming AlarmManager/BGTaskScheduler is battery-neutral (no process
// timer) and is the only way a background-created deadline ever fires
// on a phone that stays backgrounded.
func TestParkedAlarmWriteArmsPlatformSlot(t *testing.T) {
	tm, f, gate := newProbeTIME(t)

	gate.Pause()
	target := time.Now().Add(45 * time.Minute).UnixMilli()
	if err := tm.SetAlarm("bg", "probe", "wall", target, nil, ""); err != nil {
		t.Fatal(err)
	}
	waitSchedulerSees(t, f, "parked SetAlarm", "schedule:"+msString(target))
}

// Cancelling the leading wall alarm re-arms the slot (canon: set,
// cancel, completion all re-arm): with nothing left, the OS alarm is
// cleared rather than left to fire into a no-op.
func TestCancelAlarmReArmsSlot(t *testing.T) {
	tm, f, _ := newProbeTIME(t)

	target := time.Now().Add(45 * time.Minute).UnixMilli()
	if err := tm.SetAlarm("solo", "probe", "wall", target, nil, ""); err != nil {
		t.Fatal(err)
	}
	waitSchedulerSees(t, f, "SetAlarm", "schedule:"+msString(target))

	if err := tm.CancelAlarm("probe", "solo"); err != nil {
		t.Fatal(err)
	}
	waitSchedulerSees(t, f, "CancelAlarm", "cancel")
}

// A stopped runtime must not be woken by the OS for nothing: Stop
// clears the registered slot.
func TestStopClearsPlatformSlot(t *testing.T) {
	tm, f, _ := newProbeTIME(t)

	target := time.Now().Add(45 * time.Minute).UnixMilli()
	if err := tm.SetAlarm("last", "probe", "wall", target, nil, ""); err != nil {
		t.Fatal(err)
	}
	waitSchedulerSees(t, f, "SetAlarm", "schedule:"+msString(target))

	tm.Stop()
	waitSchedulerSees(t, f, "Stop", "cancel")
}

func msString(ms int64) string {
	return fmt.Sprintf("%d", ms)
}
