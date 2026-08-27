package dashboard

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/quiesce"
)

// The outbox drift sweep under the quiesce governor (2026-08-19, the
// battery fix). Observable: GetOutbox is the sweep pass's first act —
// its call count IS the pump's periodic activity. Three assertions:
// a parked sweep never runs; PokeOutbox (event-driven delivery) still
// pumps while parked — quiesce governs cadence, never work; resume runs
// the catch-up sweep promptly.
func TestQuiesceParksOutboxSweep(t *testing.T) {
	var calls atomic.Int32
	h := &WSHandler{
		IdentityName: "QuiesceTest",
		GetOutbox: func() ([]OutboxItem, error) {
			calls.Add(1)
			return nil, nil
		},
	}
	s := New("127.0.0.1", 0, h)
	s.sweepEvery = 20 * time.Millisecond // in-package: shrink for test (the sessionGrace pattern)
	g := quiesce.NewGate()
	g.Pause()
	s.SetQuiesceGate(g)

	if _, err := s.Start(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		s.Shutdown(ctx)
	}()

	// Parked: ≥3 sweep intervals of total silence.
	time.Sleep(6 * s.sweepEvery)
	if n := calls.Load(); n != 0 {
		t.Fatalf("parked sweep ran %d times — the ticker woke while backgrounded", n)
	}

	// Event-driven work is NOT governed: a poke pumps while parked.
	s.PokeOutbox()
	deadline := time.Now().Add(5 * time.Second)
	for calls.Load() < 1 {
		if time.Now().After(deadline) {
			t.Fatal("poke must still pump while parked — quiesce governs cadence, not work")
		}
		time.Sleep(5 * time.Millisecond)
	}
	base := calls.Load()

	// Resume: the catch-up tick sweeps promptly, then cadence.
	g.Resume()
	deadline = time.Now().Add(5 * time.Second)
	for calls.Load() <= base {
		if time.Now().After(deadline) {
			t.Fatal("resume catch-up sweep never ran")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
