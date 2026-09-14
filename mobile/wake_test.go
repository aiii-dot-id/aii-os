package mobile

// .
// .
// .
// .
// .
// .

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

// .
// .
type fakeScheduler struct {
	mu     sync.Mutex
	events []string
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

// .
// .
type acceptOwner struct{ name string }

func (o acceptOwner) Name() string { return o.name }
func (o acceptOwner) OnAlarm(ctx context.Context, alarmID, clock string, deadline int64, payload string) cognitive.AlarmResult {
	return cognitive.AlarmResult{Accepted: true}
}

// .
// .
// .
// .
func TestWakeSchedulerLearnsNextDue(t *testing.T) {
	st, err := store.New(filepath.Join(t.TempDir(), "wake.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	tm := cognitive.NewTIME(st, st)
	f := &fakeScheduler{}
	// .
	// .
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

	// .
	waitFor("idle start", "cancel")

	// .
	d1 := cognitive.WallNow() + int64(time.Hour/time.Millisecond)
	if err := tm.SetAlarm("brief", "brief", "wall", d1, nil, ""); err != nil {
		t.Fatal(err)
	}
	waitFor("armed", fmt.Sprintf("schedule:%d", d1))

	// .
	d2 := d1 - int64(30*time.Minute/time.Millisecond)
	if err := tm.SetAlarm("brief", "brief", "wall", d2, nil, ""); err != nil {
		t.Fatal(err)
	}
	waitFor("rescheduled", fmt.Sprintf("schedule:%d", d2))

	// .
	// .
	// .
	d3 := cognitive.WallNow() + 300
	if err := tm.SetAlarm("brief", "brief", "wall", d3, nil, ""); err != nil {
		t.Fatal(err)
	}
	waitFor("fired out", "cancel")
}
