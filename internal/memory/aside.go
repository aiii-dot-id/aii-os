package memory

import "sync"

type TextFault interface {
	error

	OfTheText() bool

	EndsThePass() bool
}

type Epocher interface {
	Epoch() any
}

type asideKey struct{ store, id string }

type asideSet struct {
	mu    sync.Mutex
	basis string
	epoch any
	rows  map[asideKey]string
	last  string
}

func (a *asideSet) begin(basis string, epoch any) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.basis != basis || a.epoch != epoch {
		a.basis, a.epoch, a.rows, a.last = basis, epoch, nil, ""
	}
}

func (a *asideSet) add(store, id, text, why string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.rows == nil {
		a.rows = map[asideKey]string{}
	}
	a.rows[asideKey{store, id}] = text
	a.last = why
}

func (a *asideSet) in(store string) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	n := 0
	for k := range a.rows {
		if k.store == store {
			n++
		}
	}
	return n
}

func (a *asideSet) without(store string, candidates []Hit) []Hit {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.rows) == 0 {
		return candidates
	}
	kept := candidates[:0:0]
	for _, h := range candidates {
		key := asideKey{store, h.ID}
		text, aside := a.rows[key]
		if aside && text != textSHA(h.Text) {
			delete(a.rows, key)
			aside = false
		}
		if !aside {
			kept = append(kept, h)
		}
	}
	return kept
}

func (f *Facility) SetAside() (count int, last string) {
	f.aside.mu.Lock()
	defer f.aside.mu.Unlock()
	return len(f.aside.rows), f.aside.last
}
