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

// .
// .
// .
// .

// .
type pwRecordingWake struct {
	mu   sync.Mutex
	last int64
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

// .
// .
// .
// .
// .
func TestSetPlatformWakeSurvivesFirstbootOrdering(t *testing.T) {
	a := New(&Config{})
	f := &pwRecordingWake{}
	a.SetPlatformWake(f)

	st, err := store.New(filepath.Join(t.TempDir(), "wake.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	a.timeFac = cognitive.NewTIME(st, st)
	a.installPlatformWake()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer a.timeFac.Stop()
	a.timeFac.Start(ctx)

	// .
	// .
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

	// .
	// .
	a.SetPlatformWake(nil)
}

// .
// .
// .
func TestSetPlatformWakeInstallsLive(t *testing.T) {
	a := New(&Config{})
	st, err := store.New(filepath.Join(t.TempDir(), "wake.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	a.timeFac = cognitive.NewTIME(st, st)
	a.installPlatformWake()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer a.timeFac.Stop()
	a.timeFac.Start(ctx)

	a.timeFac.RegisterOwner(pwOwner{})
	deadline := cognitive.WallNow() + int64(time.Hour/time.Millisecond)
	if err := a.timeFac.SetAlarm("pw", "pw", "wall", deadline, nil, ""); err != nil {
		t.Fatal(err)
	}

	// .
	// .
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
