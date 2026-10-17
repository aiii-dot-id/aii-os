package foreground

import (
	"runtime"
	"testing"
	"time"
)

// THE DELIVERER ENDS WHEN NOBODY LISTENS. Each app owns a registry, and a
// mobile in-process restart makes a new app: the old registry's deliverer used
// to range forever over a wake channel nobody closes.
func TestTheDelivererEndsWhenNobodyListens(t *testing.T) {
	before := runtime.NumGoroutine()
	for i := 0; i < 10; i++ {
		var h Holds
		h.Subscribe(func(bool, string) {})
		h.Subscribe(nil)
	}
	for deadline := time.Now().Add(5 * time.Second); runtime.NumGoroutine() > before && time.Now().Before(deadline); {
		time.Sleep(10 * time.Millisecond)
	}
	if extra := runtime.NumGoroutine() - before; extra > 0 {
		t.Fatalf("%d deliverers outlived their subscribers", extra)
	}
}

// A SUBSCRIBER AFTER ONE LEFT HEARS EVERY EDGE IN ORDER: one deliverer at a
// time, started again by the next Subscribe.
func TestEdgesStayInOrderAcrossAResubscribe(t *testing.T) {
	var h Holds
	h.Subscribe(func(bool, string) {})
	h.Subscribe(nil)
	got := make(chan bool, 16)
	h.Subscribe(func(needed bool, _ string) { got <- needed })
	h.Acquire("a")()
	h.Acquire("b")()
	want := []bool{false, true, false, true, false} // catch-up, then two holds
	for i, w := range want {
		select {
		case g := <-got:
			if g != w {
				t.Fatalf("edge %d: got %v, want %v", i, g, w)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("edge %d never arrived", i)
		}
	}
}
