// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
package quiesce

import (
	"sync"
	"time"
)

// .
// .
// .
// .
// .
// .
type Gate struct {
	mu      sync.Mutex
	paused  bool
	tickers map[*Ticker]struct{}
	hooks   []func()
}

// .
func NewGate() *Gate {
	return &Gate{tickers: make(map[*Ticker]struct{})}
}

// .
// .
func (g *Gate) Pause() {
	if g == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.paused {
		return
	}
	g.paused = true
	for t := range g.tickers {
		t.pause()
	}
	for _, fn := range g.hooks {
		fn()
	}
}

// .
// .
func (g *Gate) Resume() {
	if g == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.paused {
		return
	}
	g.paused = false
	for t := range g.tickers {
		t.resume()
	}
	for _, fn := range g.hooks {
		fn()
	}
}

// .
func (g *Gate) Paused() bool {
	if g == nil {
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.paused
}

// .
// .
// .
// .
// .
// .
func (g *Gate) OnTransition(fn func()) {
	if g == nil || fn == nil {
		return
	}
	g.mu.Lock()
	g.hooks = append(g.hooks, fn)
	g.mu.Unlock()
}

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
type Ticker struct {
	C <-chan time.Time

	c    chan time.Time
	d    time.Duration
	gate *Gate

	mu      sync.Mutex
	timer   *time.Timer
	paused  bool
	stopped bool
}

// .
// .
// .
// .
// .
func NewTicker(g *Gate, d time.Duration) *Ticker {
	if d <= 0 {
		panic("quiesce: non-positive interval for NewTicker")
	}
	c := make(chan time.Time, 1)
	t := &Ticker{C: c, c: c, d: d, gate: g}
	if g == nil {
		t.mu.Lock()
		t.arm()
		t.mu.Unlock()
		return t
	}
	// .
	// .
	// .
	// .
	g.mu.Lock()
	g.tickers[t] = struct{}{}
	t.mu.Lock()
	t.paused = g.paused
	if !t.paused {
		t.arm()
	}
	t.mu.Unlock()
	g.mu.Unlock()
	return t
}

// .
// .
func (t *Ticker) Stop() {
	if t.gate != nil {
		t.gate.mu.Lock()
		delete(t.gate.tickers, t)
		t.gate.mu.Unlock()
	}
	t.mu.Lock()
	t.stopped = true
	if t.timer != nil {
		t.timer.Stop()
	}
	t.mu.Unlock()
}

// .
func (t *Ticker) arm() {
	if t.timer == nil {
		t.timer = time.AfterFunc(t.d, t.fire)
	} else {
		t.timer.Reset(t.d)
	}
}

// .
// .
func (t *Ticker) fire() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.stopped || t.paused {
		return
	}
	select {
	case t.c <- time.Now():
	default:
	}
	t.arm()
}

// .
// .

func (t *Ticker) pause() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.stopped || t.paused {
		return
	}
	t.paused = true
	if t.timer != nil {
		t.timer.Stop()
	}
	// .
	// .
	select {
	case <-t.c:
	default:
	}
}

func (t *Ticker) resume() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.stopped || !t.paused {
		return
	}
	t.paused = false
	// .
	// .
	select {
	case t.c <- time.Now():
	default:
	}
	t.arm()
}
