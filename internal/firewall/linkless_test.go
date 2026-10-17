//go:build !windows

package firewall

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A PLACE NO LINK CAN REACH IS NOT WALKED FOR A FILE'S OTHER NAMES. The
// process table is kept while the sandbox is off, and it is a file system
// the system makes up as it is read: no file outside it can be a second
// name of one inside it, and a walk of it would read every process's
// entries and stop at the first it may not read, refusing every file with
// more than one name on the account. Such a place (Rule.Linkless) is
// passed over by that walk and asked as any place by every other
// question. The control half: the same place without the mark is walked,
// stops at the entry it may not read, and refuses the file.
func TestAPlaceNoLinkCanReachIsNotWalkedForAFilesOtherNames(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("the walk's stop is an entry the account may not read, and root reads every entry")
	}
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	table, home := filepath.Join(base, "proc"), filepath.Join(base, "home")
	shut := filepath.Join(table, "1", "fd")
	for _, dir := range []string{shut, home} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(table, "1", "environ"), []byte("SECRET=1"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(shut, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(shut, 0o700) })
	twin := filepath.Join(home, "twin-a.txt")
	if err := os.WriteFile(twin, []byte("a note twice"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(twin, filepath.Join(home, "twin-b.txt")); err != nil {
		t.Skipf("hard links unavailable here: %v", err)
	}
	info, err := os.Stat(twin)
	if err != nil {
		t.Fatal(err)
	}

	for _, linkless := range []bool{true, false} {
		p := DefaultPolicy()
		p.SetPlaces([]*Rule{{ID: "sub.processes", Kind: KindSubstrate, Path: table, Reason: "kept", Enforced: true, Linkless: linkless}})
		walked := p.Check("read", twin)
		held := p.KeptAs(info, nil)
		if linkless {
			if !walked.Allowed || held != nil {
				t.Fatalf("a file with two names in the home was refused for a place no link can reach: %+v %+v", walked.Rule, held)
			}
		} else {
			if walked.Allowed || walked.Rule.Kind != KindUnasked || held == nil || held.Kind != KindUnasked {
				t.Fatalf("the control: a walked place that stops at an entry it may not read refuses a file with two names, got %+v %+v", walked.Rule, held)
			}
		}
		// Every other question asks it as any place.
		for _, path := range []string{table, filepath.Join(table, "1", "environ"), filepath.Join(shut, "3")} {
			if v := p.Check("read", path); v.Allowed || v.Rule.ID != "sub.processes" {
				t.Fatalf("linkless=%v: %s inside the place was not refused by it: %+v", linkless, path, v)
			}
			if p.Covered(path) == nil {
				t.Fatalf("linkless=%v: %s inside the place is not covered", linkless, path)
			}
		}
	}
}

// A WALK THAT CANNOT READ A STEM'S ENTRY NAMES WHERE IT STOPPED. A stem
// is how names begin in a directory, and its entries are walked for a
// file's other names; where one cannot be read, every file with more than
// one name is refused, and the refusal names the folder, from the
// directory the stem's names are in, for the operator to clear.
func TestAWalkThatCannotReadAStemsEntryNamesWhereItStopped(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("the walk's stop is an entry the account may not read, and root reads every entry")
	}
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	shut := filepath.Join(base, "tmp", "aii-plugin-x-1", "inner")
	home := filepath.Join(base, "home")
	for _, dir := range []string{shut, home} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(shut, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(shut, 0o700) })
	twin := filepath.Join(home, "twin-a.txt")
	if err := os.WriteFile(twin, []byte("twice"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(twin, filepath.Join(home, "twin-b.txt")); err != nil {
		t.Skipf("hard links unavailable here: %v", err)
	}
	p := DefaultPolicy()
	p.SetPlaces([]*Rule{{ID: "sub.temp", Kind: KindSubstrate, Path: filepath.Join(base, "tmp", "aii-plugin-"), Stem: true, Reason: "kept", Enforced: true}})
	v := p.Check("read", twin)
	want := filepath.Join("tmp", "aii-plugin-x-1", "inner")
	if v.Allowed || v.Rule.Kind != KindUnasked || !strings.Contains(v.Rule.Reason, "could not read "+want+",") {
		t.Fatalf("a file with two names while a stem's entry cannot be read: refused, naming %s, got %+v", want, v.Rule)
	}
}
