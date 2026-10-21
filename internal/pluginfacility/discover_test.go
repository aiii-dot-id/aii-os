package pluginfacility

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
)

// THE FACILITY DISCOVERS. The host's scan says what is on disk; the
// facility establishes what each package is by verification, refuses
// what is ambiguous or duplicated, keeps off what policy keeps off with
// the reason on the snapshot, and re-reads a package only when its
// bytes or the trust it is judged under changed.

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
		Log: func(format string, args ...any) {
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
	verified, err := f.VerifiedPackages(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(verified) != 2 {
		t.Fatalf("storage upgrade inventory omitted a verified held package: %+v", verified)
	}
	verified[0].ID = "not the cached identity"
	if next, err := f.VerifiedPackages(ctx); err != nil || next[0].ID == verified[0].ID {
		t.Fatal("inventory readback mutated discovery evidence")
	}
	until(t, "the admitted one to serve", func() bool { return viewOf(f, idOf("one.aiiospkg")).State == StateActive })
	// The kept-off one is on the snapshot with its reason, and never
	// became an instance.
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
	// The snapshot says what is wanted.
	if !viewOf(f, idOf("one.aiiospkg")).Wanted || viewOf(f, idOf("one.aiiospkg")).Dir != "plugins/one" {
		t.Fatalf("the wanted flag and the directory reach the snapshot: %+v", viewOf(f, idOf("one.aiiospkg")))
	}
}

func TestStorageOwnershipEvidenceDoesNotTrustTheDiscoveryMemo(t *testing.T) {
	rt := newSharedRuntime()
	scan := &scanFake{}
	scan.set(Discovery{Found: []Found{
		{Dir: "plugins/one", Package: "one.aiiospkg", Size: 10, MTime: 1},
		{Dir: "plugins/low", Package: "low.aiiospkg", Size: 10, MTime: 1},
	}})
	f := New(Config{Runtime: rt, Discover: scan.get})
	f.Rescan(Policy{Revision: 1, Allows: func(Evidence) (bool, string) { return false, "held" }})
	rt.mu.Lock()
	rt.verifyErrFor["one.aiiospkg"] = errors.New("same size/mtime, no longer verifies")
	rt.mu.Unlock()
	current, err := f.VerifiedPackages(t.Context())
	if err != nil || len(current) != 1 || current[0].ID != idOf("low.aiiospkg") {
		t.Fatalf("ownership used cached instead of fresh evidence: %+v %v", current, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := f.VerifiedPackages(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("verification ignored cancellation: %v", err)
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
	base := countCalls(rt, "verify:one.aiiospkg") // the scan's, plus the attempt's own
	// Unchanged bytes under unchanged trust: the scan does not re-read.
	f.Rescan(pol)
	f.Rescan(pol)
	if got := countCalls(rt, "verify:one.aiiospkg"); got != base {
		t.Fatalf("an unchanged package was re-verified by the scan: %d, was %d", got, base)
	}
	// New bytes: re-read.
	scan.set(Discovery{Found: []Found{{Dir: "plugins/one", Package: "one.aiiospkg", Size: 11, MTime: 2}}})
	f.Rescan(pol)
	if got := countCalls(rt, "verify:one.aiiospkg"); got < base+1 {
		t.Fatalf("changed bytes are re-verified: %d, was %d", got, base)
	}
	base = countCalls(rt, "verify:one.aiiospkg")
	// Trust moved: re-read, though the bytes did not change.
	f.Rescan(Policy{Revision: 1, TrustGen: 2})
	if got := countCalls(rt, "verify:one.aiiospkg"); got < base+1 {
		t.Fatalf("a package is re-verified under a new trust generation: %d, was %d", got, base)
	}
}

func TestRescanRefusesAmbiguityAndDuplicates(t *testing.T) {
	rt := newSharedRuntime()
	scan := &scanFake{}
	// Two directories carrying the SAME verified id: the first wins.
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
		Log: func(format string, args ...any) { logMu.Lock(); logs = append(logs, format); logMu.Unlock() }})
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

// WHAT EACH INSTANCE IS TRAVELS WITH IT. A speech engine that is installed
// and not serving — warming up, fetching its models for the first time,
// backing off after a crash — is known to the rest of the host only through
// this snapshot, and the family its package declared is what makes it an
// engine there (audit H7: Speech settings were refused for exactly those
// minutes, because only a running engine was known).
func TestTheSnapshotSaysWhichFamilyEachInstanceIs(t *testing.T) {
	rt := newSharedRuntime()
	rt.familyFor["voice.aiiospkg"] = "voice_interface"
	scan := &scanFake{}
	scan.set(Discovery{Found: []Found{
		{Dir: "plugins/voice", Package: "voice.aiiospkg", Size: 10, MTime: 1},
		{Dir: "plugins/tool", Package: "tool.aiiospkg", Size: 10, MTime: 1},
	}})
	f := New(Config{Runtime: rt, Discover: scan.get}) // never attached: nothing starts
	t.Cleanup(f.Close)
	f.Rescan(Policy{Revision: 1})
	if v := viewOf(f, idOf("voice.aiiospkg")); !v.Wanted || v.Family != "voice_interface" {
		t.Fatalf("the engine's family is on its instance from the scan: %+v", v)
	}
	if v := viewOf(f, idOf("tool.aiiospkg")); !v.Wanted || v.Family != "" {
		t.Fatalf("a package that declares no family claims none: %+v", v)
	}

	// A facility that is handed its observations keeps the family across a
	// pass that only moves the policy.
	handed := New(Config{Runtime: rt})
	t.Cleanup(handed.Close)
	handed.Observe([]Observed{{ID: "id.example.voice", Package: "voice.aiiospkg", Hash: "h", Family: "voice_interface"}}, Policy{Revision: 1})
	handed.Rescan(Policy{Revision: 2})
	if v := viewOf(handed, "id.example.voice"); !v.Wanted || v.Family != "voice_interface" {
		t.Fatalf("a policy pass dropped what the instance is: %+v", v)
	}
}

// A LISTING THAT FAILED IS NOT AN EMPTY DIRECTORY. The host's scan answered
// a plugins directory it could not open with nothing found, and the
// facility withdrew every plugin mid-use and forgot what was installed.
// What was observed last stands until a listing reads again; then a
// package that really left is withdrawn as before.
func TestAListingThatFailedWithdrawsNothing(t *testing.T) {
	rt := newSharedRuntime()
	scan := &scanFake{}
	there := Discovery{Found: []Found{{Dir: "plugins/one", Package: "one.aiiospkg", Size: 10, MTime: 1}}}
	scan.set(there)
	var logs []string
	var logMu sync.Mutex
	f := New(Config{Runtime: rt, Capacity: ampleCapacity{}, Discover: scan.get,
		Log: func(format string, args ...any) {
			logMu.Lock()
			logs = append(logs, strings.TrimSpace(format))
			logMu.Unlock()
		}})
	ctx, cancel := context.WithCancel(context.Background())
	f.Attach(ctx)
	t.Cleanup(func() { cancel(); f.Close() })
	pol := Policy{Revision: 1, Allows: func(Evidence) (bool, string) { return true, "" }}
	id := idOf("one.aiiospkg")
	f.Rescan(pol)
	until(t, "the plugin to serve", func() bool { return viewOf(f, id).State == StateActive })

	// The directory cannot be opened: three sweeps in a row.
	scan.set(Discovery{Err: errors.New("open plugins: too many open files")})
	for i := 0; i < 3; i++ {
		f.Rescan(pol)
	}
	if v := viewOf(f, id); !v.Wanted || !v.Installed || v.State != StateActive {
		t.Fatalf("A PLUGIN WAS WITHDRAWN BECAUSE ITS DIRECTORY COULD NOT BE LISTED: wanted=%v installed=%v state=%v", v.Wanted, v.Installed, v.State)
	}
	logMu.Lock()
	said := 0
	for _, l := range logs {
		if strings.Contains(l, "could not be listed") {
			said++
		}
	}
	logMu.Unlock()
	if said != 1 {
		t.Errorf("a listing that failed three times was said %d times; want once", said)
	}

	// It reads again, and the package is gone: now it is withdrawn.
	scan.set(Discovery{})
	f.Rescan(pol)
	until(t, "a package that really left to be withdrawn", func() bool { return !viewOf(f, id).Wanted })
}
