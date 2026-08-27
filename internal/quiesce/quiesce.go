// Package quiesce is the background-metabolism governor (2026-08-19).
//
// This package exists because a phone died: the daemon's periodic loops
// — two 2s mtime polls, a 500ms work-queue poll, 60s sweeps, the
// heartbeat — kept their tickers firing while the mobile shell was
// backgrounded. A firing ticker still wakes the CPU even when the loop
// body is a no-op, and a few hundred thousand wakeups a day is a dead
// battery. So: when the shell reports background, every periodic loop
// PARKS — the underlying timer is STOPPED, not skipped (zero wakeups
// while parked) — and on foreground everything resumes with one
// immediate catch-up tick, then normal cadence.
//
// The catch-up law: at most ONE tick per Resume, never a backlog
// replay. Each governed loop's body is already idempotent drift
// insurance (config mtime check, layout mtime check, outbox sweep,
// queue poll) — one pass after a gap covers the whole gap, so nothing
// is missed, only deferred.
//
// SAFE interplay: none, by design. SAFE is about writes; quiesce is
// about wakeups — SAFE never pauses the gate and the gate never enters
// or leaves SAFE (orthogonal switches; even the SAFE beacon's TICKER
// parks while backgrounded, but SAFE itself stands).
//
// Desktop is untouched: the gate starts RUNNING and only the mobile
// shell's SetForeground(false) ever pauses it. This is the
// stop-the-bleeding half; OS-scheduled background wakes (AlarmManager /
// BGTaskScheduler via PlatformWake + TimeWake) are the other half and
// compose with this one — an invited OS wake still runs, quiesce only
// stops the self-inflicted ones.
package quiesce

import (
	"sync"
	"time"
)

// Gate is the one foreground/background switch. The app owns exactly
// one, created RUNNING (paused-by-default would slow every desktop
// process and every test for a mobile-only problem). Safe for
// concurrent use; Pause when paused and Resume when running are no-ops.
// A nil *Gate behaves as always-running, so packages holding one stay
// usable without an app.
type Gate struct {
	mu      sync.Mutex
	paused  bool
	tickers map[*Ticker]struct{}
	hooks   []func()
}

// NewGate creates a running gate.
func NewGate() *Gate {
	return &Gate{tickers: make(map[*Ticker]struct{})}
}

// Pause parks every governed ticker: underlying timers stopped, C
// silent. Idempotent; nil-safe.
func (g *Gate) Pause() {
	if g == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock() // held across transitions so Pause/Resume serialize (lock order: Gate → Ticker)
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

// Resume unparks every governed ticker: each fires ONE immediate
// catch-up tick, then resumes cadence. Idempotent; nil-safe.
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

// Paused reports the gate state. Nil-safe (a nil gate never pauses).
func (g *Gate) Paused() bool {
	if g == nil {
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.paused
}

// OnTransition registers fn to run after every Pause/Resume flip. It
// runs under the gate's lock: keep it tiny and non-blocking, and never
// call back into the Gate. This exists for the one governed loop that
// is not ticker-shaped — TIME's one-timer scheduler pokes its resched
// channel here so a pending select re-reads Paused() and parks or
// unparks. Everything ticker-shaped uses NewTicker instead.
func (g *Gate) OnTransition(fn func()) {
	if g == nil || fn == nil {
		return
	}
	g.mu.Lock()
	g.hooks = append(g.hooks, fn)
	g.mu.Unlock()
}

// Ticker is a governed time.Ticker: range the same C, same drop-on-slow
// semantics (capacity-1 channel), same Stop for shutdown. The one
// difference is the gate: on Pause the underlying timer is STOPPED —
// out of the runtime timer heap, zero wakeups — and any undelivered
// tick is discarded (parked means silent, not deferred-by-one). On
// Resume, C carries one immediate catch-up tick before Resume even
// returns, then cadence restarts. Cadence is re-armed after each fire,
// so the period is interval-plus-epsilon rather than time.Ticker's
// drift-corrected interval — these are drift-insurance polls, not
// metronomes.
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

// NewTicker returns a governed ticker on gate g. The one-line loop
// change: time.NewTicker(d) becomes quiesce.NewTicker(gate, d), the
// select shape stays. A nil gate yields an always-running ticker
// (packages usable without an app). Panics on non-positive d, exactly
// like time.NewTicker.
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
	// Register and take the gate's current state in ONE critical
	// section: a ticker born under a paused gate starts parked, and a
	// concurrent Pause/Resume cannot slip between registration and the
	// first arm.
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

// Stop shuts the ticker down for good: deregisters from the gate, stops
// the timer. Like time.Ticker.Stop it does not close C.
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

// arm starts or re-arms the underlying timer. Caller holds t.mu.
func (t *Ticker) arm() {
	if t.timer == nil {
		t.timer = time.AfterFunc(t.d, t.fire)
	} else {
		t.timer.Reset(t.d)
	}
}

// fire delivers one tick and re-arms. A fire that raced a Pause or Stop
// (timer went off before Stop landed) is dropped — parked means silent.
func (t *Ticker) fire() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.stopped || t.paused {
		return
	}
	select {
	case t.c <- time.Now():
	default: // slow receiver: drop, like time.Ticker
	}
	t.arm()
}

// pause and resume are driven by the Gate, which holds its own lock
// across the call (lock order Gate → Ticker, everywhere).

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
	// Discard an undelivered tick from before the pause: after Pause
	// returns, C is silent until Resume — no stale wakeup sneaks in.
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
	// The catch-up: exactly one immediate tick (at most one — the
	// channel holds one), then cadence.
	select {
	case t.c <- time.Now():
	default:
	}
	t.arm()
}
