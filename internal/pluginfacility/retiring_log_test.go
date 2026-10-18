package pluginfacility

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestAnOwedRetirementIsLoggedOnceNotPerAsking(t *testing.T) {
	var mu sync.Mutex
	var lines []string
	rt := newSharedRuntime()
	rt.stopPendingFor["one.aiiospkg"] = true
	f := New(Config{Runtime: rt, Capacity: ampleCapacity{}, Log: func(format string, args ...any) {
		mu.Lock()
		lines = append(lines, fmt.Sprintf(format, args...))
		mu.Unlock()
	}})
	auditAttachFacility(t, f)
	id := idOf("one.aiiospkg")
	f.Observe(obs("one.aiiospkg"), Policy{Revision: 1})
	auditWaitFacility(t, "it to serve", func() bool { return viewOf(f, id).State == StateActive })
	f.Observe(nil, Policy{Revision: 1})
	auditWaitFacility(t, "it to drain", func() bool { return viewOf(f, id).State == StateDraining })
	time.Sleep(900 * time.Millisecond) // 250 ms, 500 ms, …: asked at least twice
	if n := strings.Count(rt.seen(), "stop:one.aiiospkg"); n < 2 {
		t.Fatalf("fixture: asked to stop %d times; the case needs the asking repeated", n)
	}
	count := func(s string) int {
		mu.Lock()
		defer mu.Unlock()
		n := 0
		for _, l := range lines {
			if strings.Contains(l, s) {
				n++
			}
		}
		return n
	}
	if n := count("retiring — not yet established"); n != 1 {
		t.Fatalf("the owed retirement was logged %d times while it was asked again; want once", n)
	}
	rt.mu.Lock()
	rt.stopPendingFor["one.aiiospkg"] = false
	rt.mu.Unlock()
	f.Settle(id)
	auditWaitFacility(t, "its record to go", func() bool { return viewOf(f, id).ID == "" })
	// The record goes in a plan pass, and the line is said after the
	// facility's lock is let go: the line can follow the record's going by
	// a moment, so it is waited for, not counted once (the swap-race gate,
	// 0.1.16: "the retirement, once established, was not said").
	auditWaitFacility(t, "the retirement, once established, to be said", func() bool { return count(": retired") >= 1 })
}
