package quiesce

import (
	"sync"
	"testing"
	"time"
)

// .
// .
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

// .
// .
// .
func TestPausedTickerIsSilent(t *testing.T) {
	g := NewGate()
	interval := 20 * time.Millisecond
	tk := NewTicker(g, interval)
	defer tk.Stop()

	// .
	select {
	case <-tk.C:
	case <-time.After(5 * time.Second):
		t.Fatal("ticker never ticked while running")
	}
	g.Pause()

	select {
	case tick := <-tk.C:
		t.Fatalf("tick at %v while PAUSED — a parked loop must not wake", tick)
	case <-time.After(5 * interval):
	}
}

// .
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
	case <-tk.C:
	case <-time.After(5 * time.Second):
		t.Fatal("resume must wake a born-parked ticker")
	}
}

// .
// .
// .
func TestResumeCatchUpIsExactlyOne(t *testing.T) {
	g := NewGate()
	interval := 200 * time.Millisecond
	tk := NewTicker(g, interval)
	defer tk.Stop()

	g.Pause()
	time.Sleep(3 * interval)
	g.Resume()

	// .
	select {
	case <-tk.C:
	default:
		t.Fatal("catch-up tick must be in C when Resume returns")
	}
	// .
	select {
	case <-tk.C:
		t.Fatal("second tick well before one interval — catch-up must be at most one, never a backlog")
	case <-time.After(interval / 2):
	}
	// .
	select {
	case <-tk.C:
	case <-time.After(5 * time.Second):
		t.Fatal("cadence must resume after the catch-up tick")
	}
}

// .
// .
func TestPauseResumeIdempotent(t *testing.T) {
	g := NewGate()
	interval := 200 * time.Millisecond
	tk := NewTicker(g, interval)
	defer tk.Stop()

	g.Pause()
	g.Pause()
	g.Resume()
	g.Resume()

	<-tk.C
	select {
	case <-tk.C:
		t.Fatal("double Resume minted a second catch-up tick")
	case <-time.After(interval / 2):
	}
}

// .
// .
func TestStopIsTerminal(t *testing.T) {
	g := NewGate()
	interval := 20 * time.Millisecond
	tk := NewTicker(g, interval)
	tk.Stop()
	tk.Stop()

	g.Pause()
	g.Resume()

	select {
	case <-tk.C:
		t.Fatal("tick after Stop")
	case <-time.After(5 * interval):
	}
}

// .
// .
func TestNilGateAlwaysRuns(t *testing.T) {
	var g *Gate
	g.Pause()
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

// .
// .
func TestOnTransitionFires(t *testing.T) {
	g := NewGate()
	var mu sync.Mutex
	flips := 0
	g.OnTransition(func() { mu.Lock(); flips++; mu.Unlock() })

	g.Pause()
	g.Pause()
	g.Resume()

	mu.Lock()
	defer mu.Unlock()
	if flips != 2 {
		t.Fatalf("want 2 hook runs (pause, resume), got %d", flips)
	}
}

// .
// .
// .
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

	g.Resume()
	for i, tk := range tks {
		// .
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
