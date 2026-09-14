package dashboard

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/quiesce"
)

// .
// .
// .
// .
// .
// .
func TestQuiesceParksOutboxSweep(t *testing.T) {
	var calls atomic.Int32
	h := &WSHandler{
		GetOutbox: func() ([]OutboxItem, error) {
			calls.Add(1)
			return nil, nil
		},
	}
	s := New("127.0.0.1", 0, h)
	s.sweepEvery = 20 * time.Millisecond
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

	// .
	time.Sleep(6 * s.sweepEvery)
	if n := calls.Load(); n != 0 {
		t.Fatalf("parked sweep ran %d times — the ticker woke while backgrounded", n)
	}

	// .
	s.PokeOutbox()
	deadline := time.Now().Add(5 * time.Second)
	for calls.Load() < 1 {
		if time.Now().After(deadline) {
			t.Fatal("poke must still pump while parked — quiesce governs cadence, not work")
		}
		time.Sleep(5 * time.Millisecond)
	}
	base := calls.Load()

	// .
	g.Resume()
	deadline = time.Now().Add(5 * time.Second)
	for calls.Load() <= base {
		if time.Now().After(deadline) {
			t.Fatal("resume catch-up sweep never ran")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
