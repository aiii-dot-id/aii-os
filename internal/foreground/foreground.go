// Package foreground answers one question for the platform shell:
// DOES THE PROCESS NEED TO STAY ALIVE RIGHT NOW, AND WHY.
//
// The C stack answered it first (sev_foreground.h): work with a live
// data plane holds a ref-counted foreground grip; Android backs the
// grip with Service.startForeground, iOS with BGProcessingTask, and
// the three desktop platforms need nothing because their daemons
// already run continuously. This is the Go reading, not a mirror
// (operator ruling 2026-08-26: "Go should be Go" — the C header is
// inspiration and example): the same contract — ref-counted holds,
// reasons a person can read, release-once safety — carried by one
// small registry with a single observer. The "platform backend" is
// whoever subscribes: on desktop nobody does and the registry is
// inert bookkeeping; on mobile the shell subscribes through
// mobile.Runtime and translates the 0↔1 transitions into its own OS
// calls, which stay in Kotlin/Swift where they belong.
//
// What grips in THIS runtime (the C header's "plugin with an active
// data plane", translated): a conversation turn in flight, and a
// claimed work item mid-run. Both are promises to a person — an
// answer being composed, an alarm being honored — and a promise is
// exactly what an OS suspension breaks.
//
// DELIVERY IS ORDERED, DECIDED UNDER THE LOCK, AND NEVER BLOCKS THE
// LOCK. The first shape notified outside the lock and raced: a
// release deciding "off" could be overtaken by a concurrent
// acquire's "on", leaving the shell free to suspend under a live
// grip. The second shape queued on a channel under the lock and
// deadlocked: the drainer needed the same lock the full-buffer
// sender held. This shape appends to a lock-guarded slice and pokes
// a capacity-one wake — dedup and ordering are decided where the
// state lives, delivery happens where no lock is held, and the LAST
// edge always matches the registry: delivery may lag, never lie.
package foreground

import "sync"

// edge is one 0↔1 transition, stamped when it was decided.
type edge struct {
	needed bool
	reason string
}

// Holds is the process-wide registry. The zero value is ready. A nil
// *Holds is inert — Acquire returns a working no-op release — so
// packages holding one stay usable without an app around them (the
// quiesce.Gate convention).
type Holds struct {
	mu        sync.Mutex
	seq       uint64
	active    map[uint64]string
	sub       func(needed bool, reason string)
	announced bool
	queue     []edge
	wake      chan struct{} // cap 1; nil until first Subscribe
}

// Acquire registers one reason to stay alive and returns its release.
// Release is idempotent. The subscriber hears only the 0↔1
// transitions — the OS cares whether, not how many.
func (h *Holds) Acquire(reason string) (release func()) {
	if h == nil {
		return func() {}
	}
	h.mu.Lock()
	if h.active == nil {
		h.active = make(map[uint64]string)
	}
	h.seq++
	id := h.seq
	h.active[id] = reason
	h.enqueueEdgeLocked(reason)
	h.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			h.mu.Lock()
			delete(h.active, id)
			h.enqueueEdgeLocked("")
			h.mu.Unlock()
		})
	}
}

// enqueueEdgeLocked records a transition when the registry just
// crossed 0↔1. Caller holds h.mu. Dedup (announced) and ordering
// (queue append) are decided under the same lock the state lives
// under; the wake poke is non-blocking by construction.
func (h *Holds) enqueueEdgeLocked(reason string) {
	needed := len(h.active) > 0
	if h.sub == nil || needed == h.announced {
		return
	}
	h.announced = needed
	if !needed {
		reason = ""
	}
	h.queue = append(h.queue, edge{needed: needed, reason: reason})
	select {
	case h.wake <- struct{}{}:
	default:
	}
}

// deliver drains the queue in order. The subscriber runs with no
// locks held and may call back into the registry freely.
func (h *Holds) deliver() {
	for range h.wake {
		for {
			h.mu.Lock()
			if len(h.queue) == 0 {
				h.mu.Unlock()
				break
			}
			e := h.queue[0]
			h.queue = h.queue[1:]
			fn := h.sub
			h.mu.Unlock()
			if fn != nil {
				fn(e.needed, e.reason)
			}
		}
	}
}

// Active lists the current reasons — the operator's read of the same
// truth the shell hears (dashboard stats).
func (h *Holds) Active() []string {
	if h == nil {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]string, 0, len(h.active))
	for _, r := range h.active {
		out = append(out, r)
	}
	return out
}

// Subscribe installs the single transition observer (the mobile
// shell's bridge); nil clears it. A late subscriber hears a catch-up
// edge with the current state — the shell may attach after work
// already started (the PlatformWake late-attach law).
func (h *Holds) Subscribe(fn func(needed bool, reason string)) {
	if h == nil {
		return
	}
	h.mu.Lock()
	h.sub = fn
	if fn != nil && h.wake == nil {
		// One delivery goroutine per subscribed registry — app
		// lifetime, started on first real subscriber; a registry
		// nobody watches never spends a goroutine.
		h.wake = make(chan struct{}, 1)
		go h.deliver()
	}
	if fn != nil {
		// Catch-up: flip announced so the current state enqueues as a
		// fresh edge for the new subscriber.
		h.announced = !(len(h.active) > 0)
		reason := ""
		for _, r := range h.active {
			reason = r
			break
		}
		h.enqueueEdgeLocked(reason)
	}
	h.mu.Unlock()
}
