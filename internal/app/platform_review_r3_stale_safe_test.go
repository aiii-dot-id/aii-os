// .
// .
// .
// .

package app

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/pluginfacility"
)

// .
// .
func TestReviewStaleScanCannotUndoSAFE(t *testing.T) {
	rt := &safeHoldRuntime{gates: map[string]chan struct{}{}, starts: map[string]int{}, cancelled: map[string]int{}, stops: map[string]int{}}
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	a := &App{cfg: &Config{Plugins: PluginsConfig{Autoload: "T0"}}, sweepPoke: make(chan struct{}, 4)}
	f := pluginfacility.New(pluginfacility.Config{Runtime: rt, Discover: func() pluginfacility.Discovery {
		close(entered)
		<-release
		return pluginfacility.Discovery{Found: []pluginfacility.Found{{Dir: "org.example.late", Package: "late", Size: 1, MTime: 1}}}
	}})
	a.facilityOnce.Do(func() { a.facility = f })
	ctx, cancel := context.WithCancel(context.Background())
	f.Attach(ctx)
	t.Cleanup(func() { unblock(); cancel(); f.Close(); a.resetModeForTest() })
	scanned := make(chan struct{})
	go func() { a.rescanPlugins(ctx); close(scanned) }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("scan did not start")
	}
	a.enterSafe("review: integrity check failed")
	unblock()
	select {
	case <-scanned:
	case <-time.After(3 * time.Second):
		t.Fatal("scan did not finish")
	}
	deadline := time.After(500 * time.Millisecond)
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-deadline:
			return
		case <-tick.C:
			if n := rt.count(rt.starts, "late"); n != 0 {
				_, safe := a.SafeMode()
				t.Fatalf("stale scan started %d plugin(s) after enterSafe returned; SAFE=%v snapshot=%+v", n, safe, f.Snapshot())
			}
		}
	}
}
