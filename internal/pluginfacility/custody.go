package pluginfacility

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
)

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
type Held struct {
	Name    string
	Release func() error
	err     error
}

// .
type Lease struct {
	Gen Generation

	// .
	// .
	// .
	releasing sync.Mutex

	mu       sync.Mutex
	held     []Held
	released bool
	// .
	// .
	sealed bool
	// .
	// .
	withdrawn atomic.Bool
	// .
	// .
	// .
	ctx context.Context
}

// .
func NewLease(gen Generation) *Lease {
	return &Lease{Gen: gen}
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
func (l *Lease) Hold(name string, release func() error) error {
	if l == nil {
		return nil
	}
	h := Held{Name: name, Release: release}
	l.mu.Lock()
	if !l.sealed {
		l.held = append(l.held, h)
		l.mu.Unlock()
		return nil
	}
	l.mu.Unlock()
	if err := releaseHeld(h); err != nil {
		return &SealedHoldError{Name: name, Err: err}
	}
	return &SealedHoldError{Name: name, Released: true}
}

// .
var ErrSealed = errors.New("the ledger is sealed: its activation's retirement was established and its record dropped")

// .
type SealedHoldError struct {
	Name     string
	Released bool
	Err      error
}

func (e *SealedHoldError) Error() string {
	if e.Released {
		return fmt.Sprintf("%s: %v; it was given back at once", e.Name, ErrSealed)
	}
	return fmt.Sprintf("%s: %v, and giving it back failed (%v) — its producer still owns it", e.Name, ErrSealed, e.Err)
}

func (e *SealedHoldError) Unwrap() []error { return []error{ErrSealed, e.Err} }

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
func (l *Lease) Authorized() bool { return l == nil || !l.withdrawn.Load() }

// .
func (l *Lease) Withdraw() {
	if l != nil {
		l.withdrawn.Store(true)
	}
}

// .
// .
// .
// .
func (l *Lease) Seal() bool {
	if l == nil {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.sealed {
		return true
	}
	if !l.released || len(l.held) != 0 {
		return false
	}
	l.sealed = true
	return true
}

// .
func (l *Lease) Holds() []string {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]string, 0, len(l.held))
	for _, h := range l.held {
		out = append(out, h.Name)
	}
	return out
}

// .
// .
// .
func (l *Lease) Context() context.Context {
	if l == nil {
		return context.Background()
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.ctx != nil {
		return l.ctx
	}
	return context.Background()
}

// .
// .
func (l *Lease) Release() error { return l.ReleaseContext(context.Background()) }

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
func (l *Lease) ReleaseContext(ctx context.Context) error {
	if l == nil {
		return nil
	}
	l.releasing.Lock()
	defer l.releasing.Unlock()
	return l.release(ctx)
}

// .
// .
// .
// .
// .
// .
func (l *Lease) TryReleaseContext(ctx context.Context) (ran bool, err error) {
	if l == nil {
		return true, nil
	}
	if !l.releasing.TryLock() {
		return false, nil
	}
	defer l.releasing.Unlock()
	return true, l.release(ctx)
}

// .
func (l *Lease) release(ctx context.Context) error {
	l.mu.Lock()
	l.released = true
	l.ctx = ctx
	held := append([]Held(nil), l.held...)
	n := len(l.held)
	l.mu.Unlock()

	// .
	releasedTo := len(held)
	var failure error
	for i := len(held) - 1; i >= 0; i-- {
		// .
		// .
		// .
		// .
		err := releaseHeld(held[i])
		if err != nil {
			failure = err
			held[i].err = err
			releasedTo = i
			break
		}
		releasedTo = i
	}
	if failure == nil {
		releasedTo = 0
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	l.ctx = nil
	var still []Held
	if failure != nil {
		// .
		// .
		still = append(still, held[:releasedTo+1]...)
	}
	// .
	// .
	if n < len(l.held) {
		still = append(still, l.held[n:]...)
	}
	l.held = still
	return l.residueErrLocked()
}

// .
// .
// .
func releaseHeld(h Held) (err error) {
	if h.Release == nil {
		return nil
	}
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("cleanup panicked: %v", r)
		}
	}()
	return h.Release()
}

// .
// .
// .
// .
func (l *Lease) Discharged() bool {
	if l == nil {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.released && len(l.held) == 0
}

// .
// .
// .
func (l *Lease) Released() bool {
	if l == nil {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.released
}

// .
// .
// .
// .
func (l *Lease) Residue() []string {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.residueLocked()
}

func (l *Lease) residueLocked() []string {
	if !l.released || len(l.held) == 0 {
		return nil
	}
	var out []string
	for _, h := range l.held {
		if h.err != nil {
			out = append(out, fmt.Sprintf("%s: %v", h.Name, h.err))
		}
	}
	return out
}

func (l *Lease) residueErrLocked() error {
	if len(l.held) == 0 {
		return nil
	}
	names := make([]string, 0, len(l.held))
	var errs []error
	for _, h := range l.held {
		names = append(names, h.Name)
		if h.err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", h.Name, h.err))
		}
	}
	return fmt.Errorf("retirement is not established, %s still held: %w", strings.Join(names, ", "), errors.Join(errs...))
}
