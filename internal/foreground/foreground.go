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
// .
// .
// .
// .
package foreground

import "sync"

// .
type edge struct {
	needed bool
	reason string
}

// .
// .
// .
// .
type Holds struct {
	mu        sync.Mutex
	seq       uint64
	active    map[uint64]string
	sub       func(needed bool, reason string)
	announced bool
	queue     []edge
	wake      chan struct{}
}

// .
// .
// .
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

// .
// .
// .
// .
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

// .
// .
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

// .
// .
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

// .
// .
// .
// .
func (h *Holds) Subscribe(fn func(needed bool, reason string)) {
	if h == nil {
		return
	}
	h.mu.Lock()
	h.sub = fn
	if fn != nil && h.wake == nil {
		// .
		// .
		// .
		h.wake = make(chan struct{}, 1)
		go h.deliver()
	}
	if fn != nil {
		// .
		// .
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
