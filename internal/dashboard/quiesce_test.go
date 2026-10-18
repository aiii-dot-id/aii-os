package dashboard

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/quiesce"
)

func TestQuiesceParksOutboxSweep(t *testing.T) {
	var calls atomic.Int32
	h := &WSHandler{
		TakeOutbox: func() ([]OutboxItem, func(bool), error) {
			calls.Add(1)
			return nil, func(bool) {}, nil
		},
	}
	s := New("127.0.0.1", 0, h)
	s.sweepEvery = 20 * time.Millisecond // in-package: shrink for test (the sessionGrace pattern)
	g := quiesce.NewGate()
	g.Pause()
	s.SetQuiesceGate(g)

	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		s.Shutdown(ctx)
	}()
	// A pass reads only for the pages open when it looks, so one is open:
	// its connect reads once, and every read after that is the pump's.
	dialWS(t, addr)
	waitForConns(t, s, 1)
	waitFor(t, "the page's connect never read the outbox", func() bool { return calls.Load() == 1 })

	// Parked: ≥3 sweep intervals of total silence.
	time.Sleep(6 * s.sweepEvery)
	if n := calls.Load() - 1; n != 0 {
		t.Fatalf("parked sweep ran %d times — the ticker woke while backgrounded", n)
	}

	// Event-driven work is NOT governed: a poke pumps while parked.
	s.PokeOutbox()
	deadline := time.Now().Add(5 * time.Second)
	for calls.Load() < 2 {
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
