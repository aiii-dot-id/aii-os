package app

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/cognitive"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// The host-wake passthrough (MOBILE_PORT §2): the shell registers its OS
// scheduler on the App, the App hands it to TIME. The FIRSTBOOT ordering
// is the part worth a test — registration lands before the cognitive
// runtime exists, and must survive until startLive installs it.

// pwRecordingWake records the slot traffic TIME emits.
type pwRecordingWake struct {
	mu   sync.Mutex
	last int64 // unixms of the last WakeAt; -1 after WakeClear; 0 = nothing yet
}

func (f *pwRecordingWake) WakeAt(at time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.last = at.UnixMilli()
	return nil
}

func (f *pwRecordingWake) WakeClear() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.last = -1
}

func (f *pwRecordingWake) lastMs() int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.last
}

type pwOwner struct{}

func (pwOwner) Name() string { return "pw" }
func (pwOwner) OnAlarm(ctx context.Context, alarmID, clock string, deadline int64, payload string) cognitive.AlarmResult {
	return cognitive.AlarmResult{Accepted: true}
}

// Registration precedes TIME on FIRSTBOOT (the shell calls
// SetWakeScheduler right after Start returns; birth reaches startLive
// later). SetPlatformWake before TIME exists must store, not crash;
// installPlatformWake — startLive's step — must hand the stored
// implementation to TIME, which then drives it with next-due targets.
func TestSetPlatformWakeSurvivesFirstbootOrdering(t *testing.T) {
	a := New(&Config{})
	f := &pwRecordingWake{}
	a.SetPlatformWake(f) // TIME does not exist yet: stored only

	st, err := store.New(filepath.Join(t.TempDir(), "wake.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	a.timeFac = cognitive.NewTIME(st, st)
	a.installPlatformWake() // what startLive runs before TIME.Start

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer a.timeFac.Stop()
	a.timeFac.Start(ctx)

	// TIME now drives the shell's scheduler: an armed wall alarm reaches
	// the fake as WakeAt with exactly its deadline.
	a.timeFac.RegisterOwner(pwOwner{})
	deadline := cognitive.WallNow() + int64(time.Hour/time.Millisecond)
	if err := a.timeFac.SetAlarm("pw", "pw", "wall", deadline, nil, ""); err != nil {
		t.Fatal(err)
	}
	waitUntil := time.Now().Add(5 * time.Second)
	for f.lastMs() != deadline {
		if time.Now().After(waitUntil) {
			t.Fatalf("stored platform wake never armed: want %d, last %d", deadline, f.lastMs())
		}
		time.Sleep(5 * time.Millisecond)
	}

	// Nil-safety both ways: clearing the scheduler restores the desktop
	// no-op (TIME maps nil to NoopWake) without a crash.
	a.SetPlatformWake(nil)
}

// SetPlatformWake with TIME already up (the LIVE-boot ordering, the
// common path) installs immediately and arms the slot without waiting
// for TIME's next natural pass — the one invited catch-up.
func TestSetPlatformWakeInstallsLive(t *testing.T) {
	a := New(&Config{})
	st, err := store.New(filepath.Join(t.TempDir(), "wake.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	a.timeFac = cognitive.NewTIME(st, st)
	a.installPlatformWake() // desktop default first, like startLive

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer a.timeFac.Stop()
	a.timeFac.Start(ctx)

	a.timeFac.RegisterOwner(pwOwner{})
	deadline := cognitive.WallNow() + int64(time.Hour/time.Millisecond)
	if err := a.timeFac.SetAlarm("pw", "pw", "wall", deadline, nil, ""); err != nil {
		t.Fatal(err)
	}

	// The shell registers late — after Start, after the alarm was armed
	// against the no-op. The registration itself must get the slot armed.
	f := &pwRecordingWake{}
	a.SetPlatformWake(f)
	waitUntil := time.Now().Add(5 * time.Second)
	for f.lastMs() != deadline {
		if time.Now().After(waitUntil) {
			t.Fatalf("late-registered platform wake never armed: want %d, last %d", deadline, f.lastMs())
		}
		time.Sleep(5 * time.Millisecond)
	}
}
