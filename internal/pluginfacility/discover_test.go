package pluginfacility

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
)

// .
// .
// .
// .
// .

type scanFake struct {
	mu   sync.Mutex
	disc Discovery
}

func (s *scanFake) get() Discovery {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.disc
}

func (s *scanFake) set(d Discovery) {
	s.mu.Lock()
	s.disc = d
	s.mu.Unlock()
}

func countCalls(rt *sharedRuntime, prefix string) int {
	n := 0
	for _, c := range strings.Split(rt.seen(), ",") {
		if strings.HasPrefix(c, prefix) {
			n++
		}
	}
	return n
}

func TestRescanObservesWhatItVerifiesAndSkipsWhatPolicyKeepsOff(t *testing.T) {
	rt := newSharedRuntime()
	scan := &scanFake{}
	scan.set(Discovery{Found: []Found{
		{Dir: "plugins/one", Package: "one.aiiospkg", Size: 10, MTime: 1},
		{Dir: "plugins/low", Package: "low.aiiospkg", Size: 10, MTime: 1},
	}})
	var logs []string
	var logMu sync.Mutex
	f := New(Config{Runtime: rt, Capacity: ampleCapacity{}, Discover: scan.get,
		Log: func(format string, args ...interface{}) {
			logMu.Lock()
			logs = append(logs, strings.TrimSpace(format))
			logMu.Unlock()
		}})
	ctx, cancel := context.WithCancel(context.Background())
	f.Attach(ctx)
	t.Cleanup(func() { cancel(); f.Close() })
	pol := Policy{Revision: 1, Allows: func(ev Evidence) (bool, string) {
		if ev.ID == idOf("low.aiiospkg") {
			return false, "verified T1 is below plugins.autoload T2"
		}
		return true, ""
	}}
	f.Rescan(pol)
	until(t, "the admitted one to serve", func() bool { return viewOf(f, idOf("one.aiiospkg")).State == StateActive })
	// .
	// .
	snap := f.Snapshot()
	if len(snap.Skips) != 1 || snap.Skips[0].ID != idOf("low.aiiospkg") || snap.Skips[0].Dir != "plugins/low" || !strings.Contains(snap.Skips[0].Reason, "below") {
		t.Fatalf("what policy keeps off is on the snapshot with its reason: %+v", snap.Skips)
	}
	if viewOf(f, idOf("low.aiiospkg")).ID != "" {
		t.Fatal("a kept-off package is not an instance")
	}
	if strings.Contains(rt.seen(), "start:low.aiiospkg") {
		t.Fatal("a kept-off package was started")
	}
	// .
	if !viewOf(f, idOf("one.aiiospkg")).Wanted || viewOf(f, idOf("one.aiiospkg")).Dir != "plugins/one" {
		t.Fatalf("the wanted flag and the directory reach the snapshot: %+v", viewOf(f, idOf("one.aiiospkg")))
	}
}

func TestRescanVerifiesOnlyWhatChanged(t *testing.T) {
	rt := newSharedRuntime()
	scan := &scanFake{}
	scan.set(Discovery{Found: []Found{{Dir: "plugins/one", Package: "one.aiiospkg", Size: 10, MTime: 1}}})
	f := New(Config{Runtime: rt, Capacity: ampleCapacity{}, Discover: scan.get})
	ctx, cancel := context.WithCancel(context.Background())
	f.Attach(ctx)
	t.Cleanup(func() { cancel(); f.Close() })
	pol := Policy{Revision: 1, TrustGen: 1}
	f.Rescan(pol)
	until(t, "the package to serve", func() bool { return viewOf(f, idOf("one.aiiospkg")).State == StateActive })
	base := countCalls(rt, "verify:one.aiiospkg")
	// .
	f.Rescan(pol)
	f.Rescan(pol)
	if got := countCalls(rt, "verify:one.aiiospkg"); got != base {
		t.Fatalf("an unchanged package was re-verified by the scan: %d, was %d", got, base)
	}
	// .
	scan.set(Discovery{Found: []Found{{Dir: "plugins/one", Package: "one.aiiospkg", Size: 11, MTime: 2}}})
	f.Rescan(pol)
	if got := countCalls(rt, "verify:one.aiiospkg"); got < base+1 {
		t.Fatalf("changed bytes are re-verified: %d, was %d", got, base)
	}
	base = countCalls(rt, "verify:one.aiiospkg")
	// .
	f.Rescan(Policy{Revision: 1, TrustGen: 2})
	if got := countCalls(rt, "verify:one.aiiospkg"); got < base+1 {
		t.Fatalf("a package is re-verified under a new trust generation: %d, was %d", got, base)
	}
}

func TestRescanRefusesAmbiguityAndDuplicates(t *testing.T) {
	rt := newSharedRuntime()
	scan := &scanFake{}
	// .
	rt.mu.Lock()
	rt.verifyErrFor["broken.aiiospkg"] = errors.New("signature does not verify")
	rt.mu.Unlock()
	scan.set(Discovery{
		Found: []Found{
			{Dir: "plugins/a", Package: "one.aiiospkg", Size: 10, MTime: 1},
			{Dir: "plugins/b", Package: "one.aiiospkg", Size: 10, MTime: 1},
			{Dir: "plugins/c", Package: "broken.aiiospkg", Size: 10, MTime: 1},
		},
		Ambiguous: []string{"plugins/two-packages"},
	})
	var logs []string
	var logMu sync.Mutex
	f := New(Config{Runtime: rt, Capacity: ampleCapacity{}, Discover: scan.get,
		Log: func(format string, args ...interface{}) { logMu.Lock(); logs = append(logs, format); logMu.Unlock() }})
	ctx, cancel := context.WithCancel(context.Background())
	f.Attach(ctx)
	t.Cleanup(func() { cancel(); f.Close() })
	f.Rescan(Policy{Revision: 1})
	until(t, "the one package to serve", func() bool { return viewOf(f, idOf("one.aiiospkg")).State == StateActive })
	if v := viewOf(f, idOf("one.aiiospkg")); v.Dir != "plugins/a" {
		t.Fatalf("the first directory providing an id wins: %q", v.Dir)
	}
	if viewOf(f, idOf("broken.aiiospkg")).ID != "" || strings.Contains(rt.seen(), "start:broken") {
		t.Fatal("a package that does not verify is not an instance and is not started")
	}
	logMu.Lock()
	joined := strings.Join(logs, "\n")
	logMu.Unlock()
	for _, want := range []string{"ambiguous, REFUSED", "duplicate REFUSED", "verification FAILED"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("the log says %q:\n%s", want, joined)
		}
	}
}
