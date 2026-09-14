package mobile

// .
// .
// .
// .
// .

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

// .
// .
// .
// .
func TestParkedAlarmWriteArmsPlatformSlot(t *testing.T) {
	tm, f, gate := newProbeTIME(t)

	gate.Pause()
	target := time.Now().Add(45 * time.Minute).UnixMilli()
	if err := tm.SetAlarm("bg", "probe", "wall", target, nil, ""); err != nil {
		t.Fatal(err)
	}
	waitSchedulerSees(t, f, "parked SetAlarm", "schedule:"+msString(target))
}

// .
// .
// .
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

// .
// .
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
