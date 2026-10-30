package updates

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// THE CHECK'S CADENCE IS THE OPERATOR'S, READ AT EACH TICK AND WHEN THE
// CONFIGURATION CHANGES (updates.check_interval_minutes). With none set it
// is the hour the code always had. A running checker follows a cadence
// changed under it at its next tick; a Rearm makes it follow at once, so a
// long cadence shortened does not wait out its period. Ticks are counted
// where the loop asks whether the identity is in SAFE (and, told it is,
// does nothing more), so no check reaches the network.
func TestTheCheckCadenceIsReadAtEachTickAndWhenRearmed(t *testing.T) {
	if got := (&Checker{}).checkInterval(); got != time.Hour {
		t.Fatalf("with no cadence set the checker waits %s; want the hour the code carried", got)
	}
	if got := (&Checker{interval: func() time.Duration { return 0 }}).checkInterval(); got != time.Hour {
		t.Fatalf("a cadence that names none is the default; got %s", got)
	}

	var ticks atomic.Int64
	c := NewChecker(nil, func() string { return "1.0.0" }, func() bool { return false }, nil, nil, t.TempDir(),
		func() bool { ticks.Add(1); return true }, func() bool { return false })
	var mu sync.Mutex
	every := 20 * time.Millisecond
	c.SetInterval(func() time.Duration { mu.Lock(); defer mu.Unlock(); return every })
	set := func(d time.Duration) { mu.Lock(); every = d; mu.Unlock() }
	waitTicks := func(n int64, why string) {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for ticks.Load() < n {
			if time.Now().After(deadline) {
				t.Fatalf("%s: %d ticks in ten seconds, want %d", why, ticks.Load(), n)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	quiet := func(why string) {
		t.Helper()
		// One tick may already be on its way when the cadence changes.
		time.Sleep(60 * time.Millisecond)
		before := ticks.Load()
		time.Sleep(300 * time.Millisecond)
		if after := ticks.Load(); after != before {
			t.Fatalf("%s: %d ticks came in 300 ms under an hour's cadence", why, after-before)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	ran := make(chan struct{})
	go func() { defer close(ran); c.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-ran })

	waitTicks(3, "at the operator's 20 ms the checker ticks")

	// Changed with no Rearm: the next tick reads it, and then ticks stop.
	set(time.Hour)
	quiet("a cadence lengthened under a running checker is read at its next tick")

	// Shortened again: under an hour's ticker only a Rearm reads it now.
	set(20 * time.Millisecond)
	n := ticks.Load()
	c.Rearm()
	waitTicks(n+3, "after a Rearm the checker follows the operator's shorter cadence at once")
}
