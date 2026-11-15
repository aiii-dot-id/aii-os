package pluginfacility

import (
	"io/fs"
	"testing"
)

// The failed stat's cases of A5's controls (unreadable_test.go). They are a
// file of their own because they need Found.Err, which the scan before A5
// does not have: on that parent this file does not compile, and the scan's
// stat skip (internal/app/plugins.go) dropped the package as absent.

func heldStat(pkg string) error {
	return &fs.PathError{Op: "GetFileAttributesEx", Path: pkg, Err: errHeld}
}

func statFails(scan *scanFake, fd Found) {
	scan.set(Discovery{Found: []Found{{Dir: fd.Dir, Package: fd.Package, Err: heldStat(fd.Package)}}})
}

// Control 1: a stat that fails, then succeeds on the same file.
func TestAPackageWhoseStatFailedIsLookedAtAgainAndServes(t *testing.T) {
	readAgainUnchanged(t, true, func(_ *sharedRuntime, scan *scanFake, fd Found) { statFails(scan, fd) })
}

// Control 2: a serving release's package cannot be looked at; it stands, and
// the file is found as it was.
func TestAServingReleaseStandsWhileItsPackageCannotBeLookedAt(t *testing.T) {
	servingStands(t, func(_ *sharedRuntime, scan *scanFake, fd Found) Found { statFails(scan, fd); return fd })
}

// ONE PACKAGE THAT CANNOT BE LOOKED AT HIDES NOTHING ELSE. While one
// package's stat fails, the scan goes on for the rest: another plugin is
// removed and withdrawn, and a third is installed and served; the one that
// could not be looked at goes on serving.
func TestAFailedStatOnOnePackageLetsTheOthersChange(t *testing.T) {
	rt, scan, logs := newSharedRuntime(), &scanFake{}, &logged{}
	one := Found{Dir: "plugins/one", Package: "one.aiiospkg", Size: 10, MTime: 1}
	two := Found{Dir: "plugins/two", Package: "two.aiiospkg", Size: 10, MTime: 1}
	three := Found{Dir: "plugins/three", Package: "three.aiiospkg", Size: 10, MTime: 1}
	f := unreadableFacility(t, rt, scan, logs)
	scan.set(Discovery{Found: []Found{one, two}})
	f.Rescan(allowAll)
	until(t, "one and two to serve", func() bool {
		return viewOf(f, idOf(one.Package)).State == StateActive && viewOf(f, idOf(two.Package)).State == StateActive
	})
	gen, _ := activeGen(viewOf(f, idOf(one.Package)))
	lease := servingLease(f, idOf(one.Package))

	scan.set(Discovery{Found: []Found{{Dir: one.Dir, Package: one.Package, Err: heldStat(one.Package)}, three}})
	f.Rescan(allowAll)
	until(t, "the removed package to be withdrawn", func() bool { return !viewOf(f, idOf(two.Package)).Wanted })
	until(t, "the installed package to serve", func() bool { return viewOf(f, idOf(three.Package)).State == StateActive })
	v := viewOf(f, idOf(one.Package))
	if g, ok := activeGen(v); !v.Wanted || v.State != StateActive || !ok || g != gen || !lease.Authorized() {
		t.Fatalf("the package that could not be looked at did not go on serving: %+v", v)
	}
	if s := unverifiedSkips(f); len(s) != 0 {
		t.Fatalf("A PACKAGE THAT COULD NOT BE LOOKED AT WAS CALLED INVALID: %+v", s)
	}
}
