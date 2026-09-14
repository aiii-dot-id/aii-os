package mobile

import (
	"context"
	"path/filepath"
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
func TestWakeSchedulerDetachMidRun(t *testing.T) {
	st, err := store.New(filepath.Join(t.TempDir(), "detach.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	tm := cognitive.NewTIME(st, st)
	f := &fakeScheduler{}
	tm.SetPlatformWake(wakeAdapter{s: f})
	tm.RegisterOwner(acceptOwner{name: "detach"})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer tm.Stop()
	tm.Start(ctx)

	far := time.Now().Add(1 * time.Hour).UnixMilli()
	if err := tm.SetAlarm("detach", "detach", "wall", far, nil, ""); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for f.last() == "" {
		if time.Now().After(deadline) {
			t.Fatalf("attached scheduler never armed")
		}
		time.Sleep(5 * time.Millisecond)
	}

	tm.SetPlatformWake(nil)
	before := len(f.events)

	if err := tm.SetAlarm("detach2", "detach", "wall", far-1_800_000, nil, ""); err != nil {
		t.Fatal(err)
	}
	// .
	// .
	time.Sleep(300 * time.Millisecond)
	f.mu.Lock()
	after := len(f.events)
	f.mu.Unlock()
	if after != before {
		t.Fatalf("detached scheduler still receiving calls: %v", f.events[before:])
	}
}
