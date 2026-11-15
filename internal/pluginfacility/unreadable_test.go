package pluginfacility

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"sync"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
)

// UNREADABLE IS NOT INVALID (A5). On Windows a package's open met a
// sharing violation, verification called it ENVELOPE_MALFORMED [open], the
// scan kept that as the package's verdict for as long as its size, time and
// trust stood still, and the serving plugin was stopped and stayed stopped
// until a restart. These are a review's
// three controls: a read that fails and then succeeds with nothing about the
// file changed; a serving release that stands through the failure; and a
// package that really is invalid or revoked, still refused. The fault is the
// runtime's verify (and the host's stat) answering with the operating
// system's typed error, which is what a Windows sharing violation reaches
// the facility as. This proves the logic on any host; the Windows machine's
// own proof is separate and owed.

// errHeld stands for the operating system's refusal: on Windows,
// ERROR_SHARING_VIOLATION. Which process held the file is not known to the
// record and not claimed here.
var errHeld = errors.New("The process cannot access the file because it is being used by another process.")

func heldOpen(pkg string) error { return &fs.PathError{Op: "open", Path: pkg, Err: errHeld} }

type logged struct {
	mu    sync.Mutex
	lines []string
}

func (l *logged) log(format string, args ...any) {
	l.mu.Lock()
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
	l.mu.Unlock()
}

func (l *logged) count(sub string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for _, s := range l.lines {
		if strings.Contains(s, sub) {
			n++
		}
	}
	return n
}

func unreadableFacility(t *testing.T, rt *sharedRuntime, scan *scanFake, logs *logged) *Facility {
	t.Helper()
	f := New(Config{Runtime: rt, Capacity: ampleCapacity{}, Discover: scan.get, Log: logs.log})
	ctx, cancel := context.WithCancel(context.Background())
	f.Attach(ctx)
	t.Cleanup(func() { cancel(); f.Close() })
	return f
}

func setVerifyErr(rt *sharedRuntime, pkg string, err error) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if err == nil {
		delete(rt.verifyErrFor, pkg)
		return
	}
	rt.verifyErrFor[pkg] = err
}

// servingLease is the lease of what serves id now, nil when nothing does.
func servingLease(f *Facility, id string) *Lease {
	f.mu.Lock()
	defer f.mu.Unlock()
	inst := f.instances[id]
	if inst == nil || inst.Active() == nil {
		return nil
	}
	return inst.Active().Lease
}

func activeGen(v InstanceView) (Generation, bool) {
	for _, a := range v.Activations {
		if a.Role == RoleActive {
			return a.Gen, true
		}
	}
	return 0, false
}

func unverifiedSkips(f *Facility) []Skip {
	var out []Skip
	for _, s := range f.Snapshot().Skips {
		if s.Kind == SkipUnverified {
			out = append(out, s)
		}
	}
	return out
}

var allowAll = Policy{Revision: 1, TrustGen: 1, Allows: func(Evidence) (bool, string) { return true, "" }}

// Control 1: a forced open failure released with nothing about the file
// changed — the same size, time and trust — and the next look serves the
// package. The failed stat's case is unreadable_stat_test.go.
func TestAPackageThatCouldNotBeOpenedIsReadAgainUnchangedAndServes(t *testing.T) {
	readAgainUnchanged(t, false, func(rt *sharedRuntime, scan *scanFake, fd Found) {
		setVerifyErr(rt, fd.Package, heldOpen(fd.Package))
		scan.set(Discovery{Found: []Found{fd}})
	})
}

func readAgainUnchanged(t *testing.T, statFailed bool, force func(rt *sharedRuntime, scan *scanFake, fd Found)) {
	t.Helper()
	rt, scan, logs := newSharedRuntime(), &scanFake{}, &logged{}
	fd := Found{Dir: "plugins/one", Package: "one.aiiospkg", Size: 10, MTime: 1}
	id := idOf(fd.Package)
	f := unreadableFacility(t, rt, scan, logs)

	force(rt, scan, fd)
	for i := 0; i < 3; i++ {
		f.Rescan(allowAll)
	}
	if v := viewOf(f, id); v.ID != "" {
		t.Fatalf("a package that could not be read became an instance: %+v", v)
	}
	if s := unverifiedSkips(f); len(s) != 0 {
		t.Fatalf("A PACKAGE THAT COULD NOT BE READ WAS CALLED INVALID: %+v", s)
	}
	if !statFailed && countCalls(rt, "verify:"+fd.Package) != 3 {
		t.Fatalf("an unreadable result was kept as a verdict: %d reads in three scans, want 3", countCalls(rt, "verify:"+fd.Package))
	}
	if statFailed && countCalls(rt, "verify:"+fd.Package) != 0 {
		t.Fatalf("a package whose stat failed was read anyway: %s", rt.seen())
	}
	if n := logs.count("could not be read"); n != 1 {
		t.Fatalf("three failed reads were said %d times; want once:\n%s", n, strings.Join(logs.lines, "\n"))
	}

	// Released, with nothing about the file changed.
	setVerifyErr(rt, fd.Package, nil)
	scan.set(Discovery{Found: []Found{fd}})
	f.Rescan(allowAll)
	until(t, "the package to serve once it reads", func() bool { return viewOf(f, id).State == StateActive })
	if s := unverifiedSkips(f); len(s) != 0 {
		t.Fatalf("a package that reads and verifies is refused: %+v", s)
	}
}

// Control 2: a release serves; its slot's package then cannot be read — a
// replacement placed beside an update (new size and time) that the open
// cannot read. What serves stands: the same activation, its lease still
// authorized, nothing withdrawn and nothing new started, through repeated
// scans; and the next look that reads goes on. The replacement recovers as
// what it is, with its own size and time, and is read for that. The failed
// stat's case is unreadable_stat_test.go.
func TestAServingReleaseStandsWhileItsPackageCannotBeOpened(t *testing.T) {
	servingStands(t, func(rt *sharedRuntime, scan *scanFake, fd Found) Found {
		setVerifyErr(rt, fd.Package, heldOpen(fd.Package))
		fd.Size, fd.MTime = 11, 2 // the update placed new bytes
		scan.set(Discovery{Found: []Found{fd}})
		return fd
	})
}

func servingStands(t *testing.T, force func(rt *sharedRuntime, scan *scanFake, fd Found) (after Found)) {
	t.Helper()
	rt, scan, logs := newSharedRuntime(), &scanFake{}, &logged{}
	fd := Found{Dir: "plugins/one", Package: "one.aiiospkg", Size: 10, MTime: 1}
	id := idOf(fd.Package)
	f := unreadableFacility(t, rt, scan, logs)
	scan.set(Discovery{Found: []Found{fd}})
	f.Rescan(allowAll)
	until(t, "the predecessor to serve", func() bool { return viewOf(f, id).State == StateActive })
	gen, _ := activeGen(viewOf(f, id))
	lease := servingLease(f, id)
	if lease == nil || !lease.Authorized() {
		t.Fatal("the serving release has no authorized lease")
	}
	starts := countCalls(rt, "start:")

	after := force(rt, scan, fd)
	for i := 0; i < 3; i++ {
		f.Rescan(allowAll)
	}
	settle()
	v := viewOf(f, id)
	if !v.Wanted || !v.Installed || v.State != StateActive {
		t.Fatalf("A SERVING RELEASE WAS WITHDRAWN BECAUSE ITS PACKAGE COULD NOT BE READ: wanted=%v installed=%v state=%v", v.Wanted, v.Installed, v.State)
	}
	if g, ok := activeGen(v); !ok || g != gen {
		t.Fatalf("what serves changed through a failed read: %v -> %v", gen, g)
	}
	if !lease.Authorized() {
		t.Fatal("the serving release's lease was withdrawn by a failed read")
	}
	if got := countCalls(rt, "start:"); got != starts {
		t.Fatalf("a package that could not be read was started: %s", rt.seen())
	}
	if s := unverifiedSkips(f); len(s) != 0 {
		t.Fatalf("A PACKAGE THAT COULD NOT BE READ WAS CALLED INVALID: %+v", s)
	}

	// It reads again: still the same release, still serving.
	reads := countCalls(rt, "verify:"+fd.Package)
	setVerifyErr(rt, fd.Package, nil)
	scan.set(Discovery{Found: []Found{after}})
	f.Rescan(allowAll)
	settle()
	if v := viewOf(f, id); v.State != StateActive || !lease.Authorized() {
		t.Fatalf("the release did not go on serving once its package read: %+v", v)
	}
	if after != fd && countCalls(rt, "verify:"+fd.Package) == reads {
		t.Fatal("the replacement, readable now, was not read: an unreadable look was kept as if it had been")
	}
}

// Control 3: what really does not verify is refused as before — kept as its
// verdict while its bytes and trust stand still, a refusal on the snapshot,
// never an instance — and a serving release that is revoked under a new
// trust generation is withdrawn. The verdicts are the verifier's own typed
// errors, one of them wrapping a cause that is not the operating system's.
func TestAnInvalidOrRevokedPackageIsStillRefused(t *testing.T) {
	t.Run("invalid", func(t *testing.T) {
		rt, scan, logs := newSharedRuntime(), &scanFake{}, &logged{}
		fd := Found{Dir: "plugins/bad", Package: "bad.aiiospkg", Size: 10, MTime: 1}
		f := unreadableFacility(t, rt, scan, logs)
		setVerifyErr(rt, fd.Package, &packagefmt.Error{Reason: packagefmt.ReasonEnvelopeMalformed, Step: "tar",
			Err: fmt.Errorf("consuming member %q: %w", "x", errors.New("gzip: invalid checksum"))})
		scan.set(Discovery{Found: []Found{fd}})
		f.Rescan(allowAll)
		f.Rescan(allowAll)
		if v := viewOf(f, idOf(fd.Package)); v.ID != "" {
			t.Fatalf("an invalid package became an instance: %+v", v)
		}
		s := unverifiedSkips(f)
		if len(s) != 1 || s[0].Package != fd.Package || !strings.Contains(s[0].Reason, "ENVELOPE_MALFORMED") {
			t.Fatalf("an invalid package is refused on the snapshot with its reason: %+v", s)
		}
		if n := countCalls(rt, "verify:"+fd.Package); n != 1 {
			t.Fatalf("a verdict is kept while the bytes stand still: %d reads, want 1", n)
		}
		if logs.count("could not be read") != 0 {
			t.Fatal("an invalid package was said to be unreadable")
		}
	})
	t.Run("revoked while serving", func(t *testing.T) {
		rt, scan, logs := newSharedRuntime(), &scanFake{}, &logged{}
		fd := Found{Dir: "plugins/one", Package: "one.aiiospkg", Size: 10, MTime: 1}
		id := idOf(fd.Package)
		f := unreadableFacility(t, rt, scan, logs)
		scan.set(Discovery{Found: []Found{fd}})
		f.Rescan(allowAll)
		until(t, "the release to serve", func() bool { return viewOf(f, id).State == StateActive })
		lease := servingLease(f, id)

		setVerifyErr(rt, fd.Package, &packagefmt.Error{Reason: packagefmt.ReasonTrustPayloadRevoked, Step: "revocation",
			Err: errors.New("the manifest's digest is in the certifier's revocation snapshot")})
		revoked := allowAll
		revoked.TrustGen = 2 // a new snapshot landed
		f.Rescan(revoked)
		until(t, "the revoked release to be withdrawn", func() bool { return !viewOf(f, id).Wanted })
		if lease == nil || lease.Authorized() {
			t.Fatal("a revoked release's lease still admits calls")
		}
		if s := unverifiedSkips(f); len(s) != 1 || !strings.Contains(s[0].Reason, "TRUST_PAYLOAD_REVOKED") {
			t.Fatalf("the revoked package is refused on the snapshot: %+v", s)
		}
	})
}

// ONCE A SPELL, AND NO DECISION ON ITS WORDING. A package that cannot be read
// is said once, from the first look that cannot read it until a look that
// reads it: a different error meeting the same spell is not said again (the
// line decides nothing by what an error says, test/error_prose_test.go), and
// a new spell after the package read is said again.
func TestAnUnreadablePackageIsSaidOnceASpell(t *testing.T) {
	rt, scan, logs := newSharedRuntime(), &scanFake{}, &logged{}
	fd := Found{Dir: "plugins/one", Package: "one.aiiospkg", Size: 10, MTime: 1}
	id := idOf(fd.Package)
	f := unreadableFacility(t, rt, scan, logs)
	scan.set(Discovery{Found: []Found{fd}})
	setVerifyErr(rt, fd.Package, heldOpen(fd.Package))
	f.Rescan(allowAll)
	f.Rescan(allowAll)
	setVerifyErr(rt, fd.Package, &fs.PathError{Op: "read", Path: fd.Package, Err: errors.New("a lock on a portion of the file")})
	f.Rescan(allowAll)
	if n := logs.count("could not be read"); n != 1 {
		t.Fatalf("one spell of three failed looks, two errors, was said %d times; want once:\n%s", n, strings.Join(logs.lines, "\n"))
	}
	setVerifyErr(rt, fd.Package, nil)
	f.Rescan(allowAll)
	until(t, "the package to serve once it reads", func() bool { return viewOf(f, id).State == StateActive })
	setVerifyErr(rt, fd.Package, heldOpen(fd.Package))
	fd.Size, fd.MTime = 11, 2 // new bytes, read for the first time and not readable
	scan.set(Discovery{Found: []Found{fd}})
	f.Rescan(allowAll)
	if n := logs.count("could not be read"); n != 2 {
		t.Fatalf("a new spell after the package read was not said: %d lines, want 2:\n%s", n, strings.Join(logs.lines, "\n"))
	}
}

// AN OLD REFUSAL IS NOT A VERDICT ON NEW BYTES. A package was refused; the
// operator places a corrected one (new size and time) and its open cannot
// read it yet. The old refusal is not shown for the new file: shown, it was
// also what judgeKept counts as a refused current package. When the new
// file reads, it is judged on its own bytes and serves.
func TestAnOldRefusalIsNotCarriedOntoNewBytesThatCannotBeRead(t *testing.T) {
	rt, scan, logs := newSharedRuntime(), &scanFake{}, &logged{}
	fd := Found{Dir: "plugins/one", Package: "one.aiiospkg", Size: 10, MTime: 1}
	id := idOf(fd.Package)
	f := unreadableFacility(t, rt, scan, logs)
	setVerifyErr(rt, fd.Package, &packagefmt.Error{Reason: packagefmt.ReasonPublisherSigInvalid, Step: "publisher-sig",
		Err: errors.New("signature does not verify")})
	scan.set(Discovery{Found: []Found{fd}})
	f.Rescan(allowAll)
	if s := unverifiedSkips(f); len(s) != 1 || s[0].Package != fd.Package {
		t.Fatalf("the old bytes are refused: %+v", s)
	}

	replaced := fd
	replaced.Size, replaced.MTime = 12, 3
	setVerifyErr(rt, fd.Package, heldOpen(fd.Package))
	scan.set(Discovery{Found: []Found{replaced}})
	reads := countCalls(rt, "verify:"+fd.Package)
	f.Rescan(allowAll)
	f.Rescan(allowAll)
	if s := unverifiedSkips(f); len(s) != 0 {
		t.Fatalf("AN OLD REFUSAL WAS SHOWN FOR NEW BYTES NOBODY READ: %+v", s)
	}
	if got := countCalls(rt, "verify:"+fd.Package) - reads; got != 2 {
		t.Fatalf("the new bytes are read at every look while unreadable: %d reads in two scans, want 2", got)
	}
	if v := viewOf(f, id); v.ID != "" {
		t.Fatalf("new bytes nobody read became an instance: %+v", v)
	}

	setVerifyErr(rt, fd.Package, nil)
	f.Rescan(allowAll)
	until(t, "the corrected package to serve once it reads", func() bool { return viewOf(f, id).State == StateActive })
	if s := unverifiedSkips(f); len(s) != 0 {
		t.Fatalf("the corrected package is refused: %+v", s)
	}
}

// The attempt's own verify reads the package too. A refusal there whose
// cause is the operating system's is asked again on the clock; a verdict
// waits on different bytes or trust, as it did.
func TestAnAttemptThatCouldNotReadItsPackageAsksAgainOnTheClock(t *testing.T) {
	for _, c := range []struct {
		name      string
		err       error
		transient bool
	}{
		{"unreadable", heldOpen("one.aiiospkg"), true},
		{"invalid", &packagefmt.Error{Reason: packagefmt.ReasonPublisherSigInvalid, Step: "publisher-sig", Err: errors.New("signature does not verify")}, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			rt := newSharedRuntime()
			setVerifyErr(rt, "one.aiiospkg", c.err)
			f := facilityWith(t, rt, ampleCapacity{})
			id := idOf("one.aiiospkg")
			f.Observe([]Observed{{ID: id, Dir: "plugins/one", Package: "one.aiiospkg", Hash: "sha256:one.aiiospkg"}}, allowAll)
			until(t, "the attempt to be refused", func() bool { return viewOf(f, id).Refusal != nil })
			v := viewOf(f, id)
			if v.Refusal.Stage != StageVerify {
				t.Fatalf("refused at %s, want %s: %v", v.Refusal.Stage, StageVerify, v.Refusal)
			}
			if got := v.Refusal.Class == ClassTransient; got != c.transient {
				t.Fatalf("class %s (wakes on %v), want transient=%v", v.Refusal.Class, v.Refusal.WakeOn, c.transient)
			}
			if c.transient && (v.RetryAt.IsZero() || v.RefusedForGood) {
				t.Fatalf("an unreadable package is not asked again on the clock: retry %v, for good %v", v.RetryAt, v.RefusedForGood)
			}
			if !c.transient && !v.RefusedForGood {
				t.Fatal("a verdict at the verify stage is no longer final for these bytes")
			}
		})
	}
}
