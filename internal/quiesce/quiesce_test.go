package quiesce

import (
	"sync"
	"testing"
	"time"
)

// Cadence while running: a governed ticker on a running gate is a
// ticker — ticks keep coming.
func TestTickerCadenceWhileRunning(t *testing.T) {
	g := NewGate()
	tk := NewTicker(g, 20*time.Millisecond)
	defer tk.Stop()

	deadline := time.After(5 * time.Second)
	for i := 0; i < 3; i++ {
		select {
		case <-tk.C:
		case <-deadline:
			t.Fatalf("only %d ticks before deadline — running ticker must tick", i)
		}
	}
}

// The battery assertion: ZERO ticks while parked. Not skipped ticks —
// no ticks: the underlying timer is stopped, and an undelivered
// pre-pause tick is discarded, so C stays silent across many intervals.
func TestPausedTickerIsSilent(t *testing.T) {
	g := NewGate()
	interval := 20 * time.Millisecond
	tk := NewTicker(g, interval)
	defer tk.Stop()

	// Let it prove it was alive, then park it.
	select {
	case <-tk.C:
	case <-time.After(5 * time.Second):
		t.Fatal("ticker never ticked while running")
	}
	g.Pause()

	select {
	case tick := <-tk.C:
		t.Fatalf("tick at %v while PAUSED — a parked loop must not wake", tick)
	case <-time.After(5 * interval): // ≥3 intervals of required silence
	}
}

// A ticker born under a paused gate starts parked.
func TestTickerBornParked(t *testing.T) {
	g := NewGate()
	g.Pause()
	interval := 20 * time.Millisecond
	tk := NewTicker(g, interval)
	defer tk.Stop()

	select {
	case <-tk.C:
		t.Fatal("ticker created under a paused gate must start parked")
	case <-time.After(5 * interval):
	}

	g.Resume()
	select {
	case <-tk.C: // the catch-up
	case <-time.After(5 * time.Second):
		t.Fatal("resume must wake a born-parked ticker")
	}
}

// Resume delivers exactly ONE immediate catch-up tick — in C before
// Resume returns — then normal cadence. Never a backlog replay, however
// long the park lasted relative to the interval.
func TestResumeCatchUpIsExactlyOne(t *testing.T) {
	g := NewGate()
	interval := 200 * time.Millisecond
	tk := NewTicker(g, interval)
	defer tk.Stop()

	g.Pause()
	time.Sleep(3 * interval) // park across several would-have-been ticks
	g.Resume()

	// The catch-up is synchronous with Resume: it is already in C.
	select {
	case <-tk.C:
	default:
		t.Fatal("catch-up tick must be in C when Resume returns")
	}
	// Exactly one: no second tick before the next cadence point.
	select {
	case <-tk.C:
		t.Fatal("second tick well before one interval — catch-up must be at most one, never a backlog")
	case <-time.After(interval / 2):
	}
	// Then cadence resumes.
	select {
	case <-tk.C:
	case <-time.After(5 * time.Second):
		t.Fatal("cadence must resume after the catch-up tick")
	}
}

// Pause when paused and Resume when running are no-ops; in particular a
// double Resume must not mint a second catch-up tick.
func TestPauseResumeIdempotent(t *testing.T) {
	g := NewGate()
	interval := 200 * time.Millisecond
	tk := NewTicker(g, interval)
	defer tk.Stop()

	g.Pause()
	g.Pause() // no-op
	g.Resume()
	g.Resume() // no-op: no second catch-up

	<-tk.C // the one catch-up
	select {
	case <-tk.C:
		t.Fatal("double Resume minted a second catch-up tick")
	case <-time.After(interval / 2):
	}
}

// Stop is terminal: no ticks after, regardless of gate transitions, and
// a stopped ticker tolerates further Pause/Resume/Stop.
func TestStopIsTerminal(t *testing.T) {
	g := NewGate()
	interval := 20 * time.Millisecond
	tk := NewTicker(g, interval)
	tk.Stop()
	tk.Stop() // idempotent

	g.Pause()
	g.Resume() // must not revive it (also exercises deregistration)

	select {
	case <-tk.C:
		t.Fatal("tick after Stop")
	case <-time.After(5 * interval):
	}
}

// Nil gate: always-running ticker, and nil-gate method calls are no-ops
// — packages holding a gate stay usable without an app.
func TestNilGateAlwaysRuns(t *testing.T) {
	var g *Gate
	g.Pause() // all nil-safe
	g.Resume()
	if g.Paused() {
		t.Fatal("nil gate must read as running")
	}
	g.OnTransition(func() {})

	tk := NewTicker(nil, 20*time.Millisecond)
	defer tk.Stop()
	select {
	case <-tk.C:
	case <-time.After(5 * time.Second):
		t.Fatal("nil-gate ticker must run")
	}
}

// OnTransition hooks run on every flip — the seam TIME's one-timer
// scheduler parks through.
func TestOnTransitionFires(t *testing.T) {
	g := NewGate()
	var mu sync.Mutex
	flips := 0
	g.OnTransition(func() { mu.Lock(); flips++; mu.Unlock() })

	g.Pause()
	g.Pause() // no flip, no hook
	g.Resume()

	mu.Lock()
	defer mu.Unlock()
	if flips != 2 {
		t.Fatalf("want 2 hook runs (pause, resume), got %d", flips)
	}
}

// Concurrent Pause/Resume hammering with live consumers — the race
// detector's test. Invariant afterwards: a final Resume leaves every
// ticker ticking.
func TestConcurrentPauseResumeHammer(t *testing.T) {
	g := NewGate()
	tks := make([]*Ticker, 4)
	for i := range tks {
		tks[i] = NewTicker(g, time.Millisecond)
		defer tks[i].Stop()
	}

	stop := make(chan struct{})
	var consumers sync.WaitGroup
	for _, tk := range tks {
		consumers.Add(1)
		go func(c <-chan time.Time) {
			defer consumers.Done()
			for {
				select {
				case <-c:
				case <-stop:
					return
				}
			}
		}(tk.C)
	}

	var hammer sync.WaitGroup
	for i := 0; i < 8; i++ {
		hammer.Add(1)
		go func(n int) {
			defer hammer.Done()
			for j := 0; j < 200; j++ {
				if (n+j)%2 == 0 {
					g.Pause()
				} else {
					g.Resume()
				}
			}
		}(i)
	}
	hammer.Wait()
	close(stop)
	consumers.Wait()

	g.Resume() // whatever the hammer left, running means ticking
	for i, tk := range tks {
		// Drain a possibly stale buffered tick, then require a fresh one.
		select {
		case <-tk.C:
		default:
		}
		select {
		case <-tk.C:
		case <-time.After(5 * time.Second):
			t.Fatalf("ticker %d dead after hammering — gate state and ticker state diverged", i)
		}
	}
}
