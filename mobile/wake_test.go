package mobile

// The OS-wake binding, Go side (MOBILE_PORT §2): TIME's one PlatformWake
// slot crosses the gomobile boundary as WakeScheduler — Schedule(unixMs)
// every time the next-due moment changes, Cancel when nothing is worth
// waking for. The fake here stands exactly where the shells stand
// (AppRuntime in Kotlin, TimeWakeScheduler in Swift), wired through the
// same wakeAdapter Runtime.SetWakeScheduler installs.

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/cognitive"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// fakeScheduler records the calls a shell would translate into
// AlarmManager.set / BGTaskScheduler.submit and their cancels.
type fakeScheduler struct {
	mu     sync.Mutex
	events []string // "schedule:<unixms>" / "cancel"
}

func (f *fakeScheduler) Schedule(atUnixMs int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, fmt.Sprintf("schedule:%d", atUnixMs))
}

func (f *fakeScheduler) Cancel() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, "cancel")
}

func (f *fakeScheduler) last() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.events) == 0 {
		return ""
	}
	return f.events[len(f.events)-1]
}

// acceptOwner accepts every firing (one-shot, no reschedule) — the
// simplest legal alarm consumer.
type acceptOwner struct{ name string }

func (o acceptOwner) Name() string { return o.name }
func (o acceptOwner) OnAlarm(ctx context.Context, alarmID, clock string, deadline int64, payload string) cognitive.AlarmResult {
	return cognitive.AlarmResult{Accepted: true}
}

// The shell's scheduler learns TIME's next-due moment: Schedule on every
// arm and re-arm (superseding — ONE slot), Cancel once nothing pending
// remains. This is the contract the Kotlin/Swift implementations build
// against; the deadlines cross as UTC milliseconds, untranslated.
func TestWakeSchedulerLearnsNextDue(t *testing.T) {
	st, err := store.New(filepath.Join(t.TempDir(), "wake.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	tm := cognitive.NewTIME(st, st)
	f := &fakeScheduler{}
	// What Runtime.SetWakeScheduler installs (via App.SetPlatformWake):
	// the adapter, nothing else between TIME and the shell.
	tm.SetPlatformWake(wakeAdapter{s: f})
	tm.RegisterOwner(acceptOwner{name: "brief"})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer tm.Stop()
	tm.Start(ctx)

	waitFor := func(step, want string) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for f.last() != want {
			if time.Now().After(deadline) {
				t.Fatalf("%s: scheduler never saw %q (last %q)", step, want, f.last())
			}
			time.Sleep(5 * time.Millisecond)
		}
	}

	// Nothing schedulable at start: the shell holds no OS alarm.
	waitFor("idle start", "cancel")

	// A wall alarm an hour out: Schedule carries EXACTLY its deadline.
	d1 := cognitive.WallNow() + int64(time.Hour/time.Millisecond)
	if err := tm.SetAlarm("brief", "brief", "wall", d1, nil, ""); err != nil {
		t.Fatal(err)
	}
	waitFor("armed", fmt.Sprintf("schedule:%d", d1))

	// Rescheduled earlier (same id replaces): the one slot supersedes.
	d2 := d1 - int64(30*time.Minute/time.Millisecond)
	if err := tm.SetAlarm("brief", "brief", "wall", d2, nil, ""); err != nil {
		t.Fatal(err)
	}
	waitFor("rescheduled", fmt.Sprintf("schedule:%d", d2))

	// Replaced with a near deadline: it fires (accepted one-shot → row
	// deleted), and with nothing left pending TIME hands the shell a
	// Cancel — never a stale target for the OS to wake a quiet app on.
	d3 := cognitive.WallNow() + 300
	if err := tm.SetAlarm("brief", "brief", "wall", d3, nil, ""); err != nil {
		t.Fatal(err)
	}
	waitFor("fired out", "cancel")
}
