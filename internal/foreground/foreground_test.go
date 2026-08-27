package foreground

import (
	"sync"
	"testing"
	"time"
)

// Delivery is asynchronous by design (ordered queue, no locks held at
// the callback); the tests wait for it rather than assume it.

type recorder struct {
	mu     sync.Mutex
	events []string
}

func (r *recorder) fn(needed bool, reason string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if needed {
		r.events = append(r.events, "on:"+reason)
	} else {
		r.events = append(r.events, "off")
	}
}

func (r *recorder) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.events...)
}

func (r *recorder) waitLen(t *testing.T, n int) []string {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		got := r.snapshot()
		if len(got) >= n {
			return got
		}
		if time.Now().After(deadline) {
			t.Fatalf("waited for %d event(s), have %v", n, got)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestTransitionsFireOnEdgesOnly(t *testing.T) {
	h := &Holds{}
	rec := &recorder{}
	h.Subscribe(rec.fn)
	if got := rec.waitLen(t, 1); got[0] != "off" {
		t.Fatalf("subscribe catch-up on an idle registry must say off, got %v", got)
	}

	relA := h.Acquire("turn")
	relB := h.Acquire("work: alarm.rhythm")
	relA()
	relB()
	relB() // idempotent

	got := rec.waitLen(t, 3)
	time.Sleep(100 * time.Millisecond) // settle: no fourth edge may arrive
	got = rec.snapshot()
	want := []string{"off", "on:turn", "off"}
	if len(got) != len(want) {
		t.Fatalf("events %v, want %v — the OS cares whether, not how many", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("events %v, want %v", got, want)
		}
	}
}

func TestLateSubscribeHearsCurrentState(t *testing.T) {
	h := &Holds{}
	rel := h.Acquire("turn")
	rec := &recorder{}
	h.Subscribe(rec.fn)
	if got := rec.waitLen(t, 1); got[0] != "on:turn" {
		t.Fatalf("late subscriber catch-up: %v", got)
	}
	rel()
	if got := rec.waitLen(t, 2); got[1] != "off" {
		t.Fatalf("release after late subscribe: %v", got)
	}
}

func TestNilHoldsIsInert(t *testing.T) {
	var h *Holds
	rel := h.Acquire("anything")
	rel()
	if got := h.Active(); got != nil {
		t.Fatalf("nil registry listed %v", got)
	}
	h.Subscribe(func(bool, string) { t.Fatal("nil registry called a subscriber") })
}

// The subscriber runs with no locks held — it may call back in (the
// shell asking Active() for its notification text is the real case).
func TestSubscriberMayReenter(t *testing.T) {
	h := &Holds{}
	done := make(chan struct{}, 4)
	h.Subscribe(func(needed bool, _ string) {
		_ = h.Active()
		done <- struct{}{}
	})
	rel := h.Acquire("turn")
	rel()
	for i := 0; i < 3; i++ {
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Fatal("delivery wedged — a reentrant subscriber deadlocked the registry")
		}
	}
}

func TestActiveListsReasons(t *testing.T) {
	h := &Holds{}
	defer h.Acquire("turn")()
	defer h.Acquire("work: alarm.maintenance")()
	got := h.Active()
	if len(got) != 2 {
		t.Fatalf("Active() = %v, want both reasons", got)
	}
}

// Order is structural: under churn the delivered sequence alternates
// strictly, and the final edge matches the registry — delivery may
// lag, never lie (the race the first shape carried, the deadlock the
// second one carried).
func TestEdgesAlternateUnderChurn(t *testing.T) {
	h := &Holds{}
	rec := &recorder{}
	h.Subscribe(rec.fn)

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				rel := h.Acquire("churn")
				rel()
			}
		}()
	}
	wg.Wait()

	deadline := time.Now().Add(3 * time.Second)
	for {
		got := rec.snapshot()
		if len(got) > 0 && (got[len(got)-1] == "off") == (len(h.Active()) == 0) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("delivery never converged to the registry: %v", rec.snapshot())
		}
		time.Sleep(10 * time.Millisecond)
	}
	got := rec.snapshot()
	for i := 1; i < len(got); i++ {
		onPrev := got[i-1] != "off"
		onCur := got[i] != "off"
		if onPrev == onCur {
			t.Fatalf("edges did not alternate at %d: %v", i, got)
		}
	}
}
