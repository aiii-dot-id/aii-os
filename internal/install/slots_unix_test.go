//go:build (darwin && !ios) || (linux && !android)

package install

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"golang.org/x/sys/unix"
)

// WHATEVER HOLDS A SLOT'S NAME IS A SLOT, UNLESS IT IS A FILE. A listing
// that kept only directories and symbolic links reads a Windows junction —
// neither, to os.ReadDir since Go 1.23 — as no slot at all, so a launcher
// finds none and runs aii init over an identity whose drive is not
// connected. The rule is the one that cannot miss a kind of link: a
// directory, a link whether it resolves or not, and anything else that is
// not a file hold the name. A FIFO stands here for the kinds this system
// cannot make: like a junction, it is neither a directory nor a symbolic
// link to os.ReadDir.
func TestWhateverHoldsASlotsNameIsASlotUnlessItIsAFile(t *testing.T) {
	root := t.TempDir()
	elsewhere := t.TempDir()
	volume := filepath.Join(elsewhere, "volume")
	file := filepath.Join(elsewhere, "file")
	if err := os.Mkdir(volume, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	name := func(n int) string { return filepath.Join(root, SlotName(n)) }
	for _, err := range []error{
		os.Mkdir(name(0), 0o700),
		os.Symlink(volume, name(1)),
		os.Symlink(filepath.Join(elsewhere, "not-connected"), name(2)),
		unix.Mkfifo(name(3), 0o600),
		os.WriteFile(name(4), nil, 0o600),
		os.Symlink(file, name(5)),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}
	ns, err := Slots(root)
	if err != nil {
		t.Fatal(err)
	}
	if want := []int{0, 1, 2, 3}; !slices.Equal(ns, want) {
		t.Fatalf("Slots = %v, want %v: a directory, a link to one, a link to nothing and a FIFO hold their names; a file and a link to a file do not", ns, want)
	}
	dirs, err := SlotDirs(root)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{name(0), name(1), name(2), name(3)}; !slices.Equal(dirs, want) {
		t.Fatalf("SlotDirs = %v, want %v", dirs, want)
	}
}

// CREATE REFUSES A LINK TO NOTHING AND LEAVES IT AS IT WAS. A slot kept on
// a drive that is not connected is a link whose target is absent. os.Stat
// follows the link and says nothing is there, so Create went on to mkdir
// and failed on the link in the system's words. The name is held: the
// refusal is the one for any held name, the link still points where it
// did, and nothing was made at its target.
func TestCreateRefusesALinkToNothingAndLeavesIt(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(t.TempDir(), "not-connected")
	link := filepath.Join(root, SlotName(0))
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(root, 0); !errors.Is(err, errSlotHeld) {
		t.Fatalf("Create over a link to nothing must refuse as for a name already held, got: %v", err)
	}
	if got, err := os.Readlink(link); err != nil || got != target {
		t.Fatalf("the link must stay as it was: Readlink = %q, %v; want %q", got, err, target)
	}
	if _, err := os.Lstat(target); !os.IsNotExist(err) {
		t.Fatalf("nothing may be made where the link points: %v", err)
	}
}

// A ROOT THAT CANNOT BE MADE IS NOT A SLOT ALREADY THERE. The system says
// "exists" of a root that is itself a link to nothing, as it does of a
// slot's name taken by another Create; only the second is the name being
// held. Create says what failed and where, and never that a slot the
// account does not have "already exists".
func TestCreateUnderARootThatIsALinkToNothingDoesNotSayTheSlotExists(t *testing.T) {
	root := filepath.Join(t.TempDir(), Dir)
	target := filepath.Join(t.TempDir(), "not-connected")
	if err := os.Symlink(target, root); err != nil {
		t.Fatal(err)
	}
	_, err := Create(root, 0)
	if err == nil || errors.Is(err, errSlotHeld) {
		t.Fatalf("Create under a root that is a link to nothing must fail, and not as for a slot's name already held, got: %v", err)
	}
	if _, err := os.Lstat(target); !os.IsNotExist(err) {
		t.Fatalf("nothing may be made where the root's link points: %v", err)
	}
}
