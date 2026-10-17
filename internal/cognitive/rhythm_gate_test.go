package cognitive

import (
	"context"
	"sync"
	"testing"
	"time"
)

// mutexGate is the turn gate's shape in production: take it or fail.
type mutexGate struct{ mu sync.Mutex }

func (g *mutexGate) TryBeginTurn() bool { return g.mu.TryLock() }
func (g *mutexGate) EndTurn()           { g.mu.Unlock() }

// heldPassOwner runs a pass that holds until released.
type heldPassOwner struct {
	entered, release chan struct{}
	runs             int
}

func (h *heldPassOwner) Name() string { return "dream" }
func (h *heldPassOwner) OnAlarm(ctx context.Context, alarmID, clock string, deadline int64, payload string) AlarmResult {
	h.runs++
	close(h.entered)
	<-h.release
	return AlarmResult{Accepted: true}
}

func TestTwoPassesAtOnceRunOnceAndDeclineOnce(t *testing.T) {
	dream := &heldPassOwner{entered: make(chan struct{}), release: make(chan struct{})}
	r := NewRhythm(&fakeRaw{n: 2}, &mutexGate{}, dream, &fakeOwner{name: "consolidate"}, &fakeOwner{name: "self_model"}, &fakeOwner{name: "identity_review"})
	r.lastConsolidate = time.Now() // inside its spacing: the material goes to DREAM
	first := make(chan AlarmResult, 1)
	go func() { first <- r.OnAlarm(context.Background(), "rhythm", "wall", 0, "") }()
	select {
	case <-dream.entered:
	case <-time.After(10 * time.Second):
		t.Fatal("the first pass never reached DREAM")
	}
	if second := r.OnAlarm(context.Background(), "rhythm", "wall", 0, ""); second.Accepted {
		t.Fatal("a second pass ran while the first held the gate")
	}
	close(dream.release)
	if res := <-first; !res.Accepted {
		t.Fatal("the pass that held the gate did not run")
	}
	if dream.runs != 1 {
		t.Fatalf("DREAM ran %d times, want once", dream.runs)
	}
}
