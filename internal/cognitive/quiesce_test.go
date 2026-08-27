package cognitive

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/quiesce"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// The cognitive engine under the quiesce governor (2026-08-19, the
// battery fix): the work-queue poll and TIME's one-timer scheduler both
// park while backgrounded and catch up ONCE on foreground.

// gatedQueue is memQueue's mutex-guarded sibling: the quiesce tests
// read counters from the test goroutine while the executor polls.
type gatedQueue struct {
	mu     sync.Mutex
	items  []*store.WorkItem
	sweeps int
}

func (m *gatedQueue) add(i *store.WorkItem) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.items = append(m.items, i)
}
func (m *gatedQueue) sweepCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sweeps
}
func (m *gatedQueue) EnqueueWork(i *store.WorkItem) (string, error) {
	m.add(i)
	return i.ID, nil
}
func (m *gatedQueue) ClaimWork(kinds []string, now int64) (*store.WorkItem, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, it := range m.items {
		if it.State == "PENDING" {
			it.State = "CLAIMED"
			return it, nil
		}
	}
	return nil, nil
}
func (m *gatedQueue) CompleteWork(id string) error  { return nil }
func (m *gatedQueue) FailWork(id, msg string) error { return nil }
func (m *gatedQueue) SweepExpiredLeases(now int64) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sweeps++
	return 0, nil
}
func (m *gatedQueue) PendingWorkCount() (int, error) { return 0, nil }

type quiesceHandler struct{ ran chan string }

func (h *quiesceHandler) WorkKinds() []string { return []string{"quiesce.test"} }
func (h *quiesceHandler) RunWork(ctx context.Context, w *store.WorkItem) error {
	h.ran <- w.ID
	return nil
}

// Parked executor: durable work enqueued while backgrounded is DEFERRED
// — zero polls, zero sweeps, the item untouched — then the foreground
// catch-up pass claims and runs it. Deferred, never lost.
func TestQuiesceParksExecutorPoll(t *testing.T) {
	q := &gatedQueue{}
	e := NewExecutor(q)
	e.poll = 20 * time.Millisecond // in-package: shrink for test
	h := &quiesceHandler{ran: make(chan string, 1)}
	e.RegisterHandler(h)
	g := quiesce.NewGate()
	g.Pause()
	e.SetQuiesceGate(g)
	e.Start(context.Background())
	defer e.Stop()

	q.add(&store.WorkItem{ID: "w1", Kind: "quiesce.test", State: "PENDING"})

	time.Sleep(6 * e.poll) // ≥3 poll intervals of required silence
	if n := q.sweepCount(); n != 0 {
		t.Fatalf("parked executor ran %d passes — the poll ticker woke while backgrounded", n)
	}
	select {
	case id := <-h.ran:
		t.Fatalf("parked executor ran work %s", id)
	default:
	}

	g.Resume()
	select {
	case <-h.ran: // the catch-up pass claimed and ran the deferred item
	case <-time.After(5 * time.Second):
		t.Fatal("foreground catch-up pass never ran the deferred work — deferred became lost")
	}
}

// Parked TIME: the one-timer scheduler arms NOTHING while the gate is
// paused — the heartbeat/witness/rhythm cadences all wake through it,
// so this is the seam that parks them all. An ephemeral cadence goes
// silent on Pause and fires ONCE promptly on Resume (recompute from the
// new now; re-arm from now — TIME's own no-catch-up-burst law), then
// keeps cadence.
func TestQuiesceParksTIMEScheduler(t *testing.T) {
	tm, _ := newTIME(t)
	g := quiesce.NewGate()
	tm.SetQuiesceGate(g)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer tm.Stop()

	var fires atomic.Int32
	const beat = 20 * time.Millisecond
	tm.Every("quiesce:beat", beat, func() { fires.Add(1) })
	tm.Start(ctx)

	// Running: the cadence proves itself.
	deadline := time.Now().Add(5 * time.Second)
	for fires.Load() < 1 {
		if time.Now().After(deadline) {
			t.Fatal("running TIME never fired the ephemeral")
		}
		time.Sleep(5 * time.Millisecond)
	}

	g.Pause()
	time.Sleep(50 * time.Millisecond) // let an in-flight fire drain before the baseline
	base := fires.Load()
	time.Sleep(6 * beat) // ≥3 intervals of required silence
	if got := fires.Load(); got != base {
		t.Fatalf("parked TIME fired %d times — a process timer was armed while backgrounded", got-base)
	}

	g.Resume()
	deadline = time.Now().Add(5 * time.Second)
	for fires.Load() <= base {
		if time.Now().After(deadline) {
			t.Fatal("resume never fired the overdue ephemeral — catch-up lost it")
		}
		time.Sleep(5 * time.Millisecond)
	}
	// And cadence continues past the catch-up.
	deadline = time.Now().Add(5 * time.Second)
	for fires.Load() <= base+1 {
		if time.Now().After(deadline) {
			t.Fatal("cadence did not resume after the catch-up fire")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TimeWake while quiesced: the OS-scheduled wake (AlarmManager receiver,
// BGTask handler — the constructive half of the battery work) is an
// INVITED wake and must evaluate due alarms even while the scheduler is
// parked. Quiesce stops self-inflicted wakeups, never invited ones: the
// parked scheduler arms nothing on its own, and TimeWake fires the due
// row anyway.
func TestTimeWakeWhileQuiescedEvaluates(t *testing.T) {
	tm, _ := newTIME(t)
	g := quiesce.NewGate()
	tm.SetQuiesceGate(g)
	fired := make(chan int64, 4)
	tm.RegisterOwner(&recordingOwner{name: "osalarm", accepted: true, fired: fired})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer tm.Stop()
	tm.Start(ctx)

	g.Pause() // backgrounded: the one-timer scheduler parks

	// A wall alarm already due, armed while parked: the parked scheduler
	// must NOT fire it on its own — that would be a process timer waking
	// a backgrounded app.
	if err := tm.SetAlarm("osalarm", "osalarm", "wall", WallNow()-1, nil, ""); err != nil {
		t.Fatal(err)
	}
	time.Sleep(120 * time.Millisecond) // silence window: no self-wake
	select {
	case <-fired:
		t.Fatal("parked TIME fired a wall alarm on its own — the park leaked a self-inflicted wake")
	default:
	}

	// The OS said run: TimeWake is exactly what the shells' receiver and
	// handler call, and it evaluates everything due while still parked.
	tm.TimeWake()
	select {
	case <-fired:
	case <-time.After(5 * time.Second):
		t.Fatal("TimeWake while quiesced did not evaluate the due alarm — the invited wake path must run while parked")
	}
}
