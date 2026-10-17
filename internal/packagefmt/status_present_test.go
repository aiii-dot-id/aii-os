package packagefmt

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestAnIdentitysStatusFileMustBePresent(t *testing.T) {
	roots := ShippedRoots()
	set := LoadRevocationStatus(t.TempDir(), roots, newFakeGuard())
	for _, d := range RevocationDomains() {
		expectAbsent(t, set, d.RootKeyType, "must be present")
	}
	offline := LoadRevocationStatus(t.TempDir(), roots, nil)
	for _, d := range RevocationDomains() {
		if _, ok := offline.Epoch(d.RootKeyType); !ok {
			t.Fatalf("%s: a check with no identity's record lost the shipped list", d.RootKeyType)
		}
	}
}

// THE HOST PUTS THE SHIPPED FILE WHERE NONE IS, AND NOTHING ELSE. It
// writes the three files into a directory that does not exist yet, byte
// for byte what the binary carries; a second call writes nothing; a file
// that is there is never replaced, a broken one included; and a root the
// operator pinned themselves gets no file. What it wrote loads under the
// guard with every tier available and nothing added to the identity's
// mark.
func TestTheShippedStatusIsWrittenWhereNoneIs(t *testing.T) {
	roots := ShippedRoots()
	dir := filepath.Join(t.TempDir(), "trust")
	wrote, err := ProvisionShippedStatus(dir, roots)
	if err != nil || len(wrote) != len(RevocationDomains()) {
		t.Fatalf("wrote %v, %v; want the three status files", wrote, err)
	}
	for _, d := range RevocationDomains() {
		got, err := os.ReadFile(filepath.Join(dir, d.FileName))
		if err != nil || !bytes.Equal(got, shippedSnapshot(d.FileName)) {
			t.Fatalf("%s on disk is not the shipped snapshot: %v", d.FileName, err)
		}
	}
	left, err := os.ReadDir(dir)
	if err != nil || len(left) != len(RevocationDomains()) {
		t.Fatalf("the directory holds %d entries, %v; a temporary file was left behind", len(left), err)
	}
	if again, err := ProvisionShippedStatus(dir, roots); err != nil || len(again) != 0 {
		t.Fatalf("a second call wrote %v, %v", again, err)
	}
	guard := newFakeGuard()
	set := LoadRevocationStatus(dir, roots, guard)
	for _, d := range RevocationDomains() {
		if _, ok := set.Epoch(d.RootKeyType); !ok {
			t.Fatalf("%s is unavailable on an install the host provisioned: %v", d.RootKeyType, set.Describe())
		}
	}
	if guard.accepts != 0 {
		t.Fatalf("accepting what the binary shipped added %d entries to the identity's mark", guard.accepts)
	}

	// Present and broken is the operator's to mend, and stays loud.
	broken := RevocationDomains()[0]
	if err := writeFileForTest(dir, broken.FileName, "{ not a snapshot"); err != nil {
		t.Fatal(err)
	}
	if again, err := ProvisionShippedStatus(dir, roots); err != nil || len(again) != 0 {
		t.Fatalf("a broken file was replaced: wrote %v, %v", again, err)
	}
	if _, ok := LoadRevocationStatus(dir, roots, newFakeGuard()).Epoch(broken.RootKeyType); ok {
		t.Fatal("a broken status file was silently mended")
	}

	// A root of the operator's own has no shipped snapshot.
	own := fixtures(t).roots
	if wrote, err := ProvisionShippedStatus(filepath.Join(t.TempDir(), "trust"), own); err != nil || len(wrote) != 0 {
		t.Fatalf("status files were written for roots the binary did not ship: %v, %v", wrote, err)
	}
}

// A MISSING FILE IS NO WAY BACK TO AN OLDER LIST. The identity accepted a
// newer list from disk, and the file is gone: deleted, or never carried
// to the machine the identity was restored on. The host writes the
// shipped file back, and the guard refuses it for that root, because it
// is below what the identity accepted; the tier is unavailable until the
// current file is supplied, and the other roots are untouched. The same
// holds when the identity accepted a different list at the shipped one's
// own epoch. Before this the shipped list was used with no check at all.
func TestAMissingFileIsNoWayBackToAnOlderList(t *testing.T) {
	roots := ShippedRoots()
	shipped := LoadRevocationStatus(t.TempDir(), roots, nil)
	domains := RevocationDomains()
	target := domains[0].RootKeyType
	epoch, ok := shipped.Epoch(target)
	if !ok {
		t.Fatal("no shipped snapshot to test against")
	}
	sha := shipped.lookup(target).payloadSHA256

	load := func(guard *fakeGuard) *RevocationStatusSet {
		t.Helper()
		dir := filepath.Join(t.TempDir(), "trust")
		if _, err := ProvisionShippedStatus(dir, roots); err != nil {
			t.Fatal(err)
		}
		return LoadRevocationStatus(dir, roots, guard)
	}
	others := func(set *RevocationStatusSet) {
		t.Helper()
		for _, d := range domains[1:] {
			if _, ok := set.Epoch(d.RootKeyType); !ok {
				t.Fatalf("%s lost its tier to another root's refusal", d.RootKeyType)
			}
		}
	}

	newer := newFakeGuard()
	newer.epochs[target], newer.shas[target] = epoch+1, "sha256:"+string(bytes.Repeat([]byte("a"), 64))
	set := load(newer)
	expectAbsent(t, set, target, "ROLLBACK refused")
	others(set)

	forked := newFakeGuard()
	forked.epochs[target], forked.shas[target] = epoch, "sha256:"+string(bytes.Repeat([]byte("b"), 64))
	set = load(forked)
	expectAbsent(t, set, target, "FORK refused")
	others(set)

	same := newFakeGuard()
	same.epochs[target], same.shas[target] = epoch, sha
	set = load(same)
	if _, ok := set.Epoch(target); !ok || same.accepts != 0 {
		t.Fatalf("the list the identity accepted, put back, was refused or ledgered again: %v, accepts %d", set.Describe(), same.accepts)
	}
	if newer.accepts != 0 || forked.accepts != 0 {
		t.Fatal("a refused list moved the identity's mark")
	}
}
