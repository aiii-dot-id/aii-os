//go:build windows

package install

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
)

// junction makes a directory junction at link to target, as mklink /J does
// and as any account may: a junction needs no privilege, which is why it is
// what a person uses to keep a slot on another drive.
func junction(t *testing.T, link, target string) {
	t.Helper()
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput(); err != nil {
		t.Fatalf("mklink /J %s %s: %v: %s", link, target, err, out)
	}
}

// A SLOT BEHIND A JUNCTION IS A SLOT, ITS TARGET THERE OR NOT. os.ReadDir
// reports a junction as neither a directory nor a symbolic link (Go 1.23
// on), and a listing that asked for one of the two finds no slot under a
// root that holds one.
func TestASlotBehindAJunctionIsASlot(t *testing.T) {
	root := t.TempDir()
	elsewhere := t.TempDir()
	present := filepath.Join(elsewhere, "present")
	gone := filepath.Join(elsewhere, "gone")
	for _, d := range []string{present, gone} {
		if err := os.Mkdir(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	junction(t, filepath.Join(root, SlotName(0)), present)
	junction(t, filepath.Join(root, SlotName(1)), gone)
	// The drive is disconnected: the junction stays and names nothing.
	if err := os.Remove(gone); err != nil {
		t.Fatal(err)
	}
	ns, err := Slots(root)
	if err != nil {
		t.Fatal(err)
	}
	if want := []int{0, 1}; !slices.Equal(ns, want) {
		t.Fatalf("Slots = %v, want %v: a junction holds its slot's name whether its target is there or not", ns, want)
	}
	if n, err := NextSlot(root); err != nil || n != 2 {
		t.Fatalf("NextSlot = %d, %v; want 2 — a number a junction holds is not free", n, err)
	}
}

// CREATE REFUSES A JUNCTION, ITS TARGET THERE OR NOT, AND LEAVES IT. With
// the target gone os.Stat says nothing is there and CreateDirectory says
// the name exists ("Cannot create a file when that file already exists");
// the answer is the refusal for any held name, the junction stays, and
// nothing is made where it points.
func TestCreateRefusesAJunctionAndLeavesIt(t *testing.T) {
	root := t.TempDir()
	elsewhere := t.TempDir()
	present := filepath.Join(elsewhere, "present")
	gone := filepath.Join(elsewhere, "gone")
	for _, d := range []string{present, gone} {
		if err := os.Mkdir(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	junction(t, filepath.Join(root, SlotName(0)), present)
	junction(t, filepath.Join(root, SlotName(1)), gone)
	if err := os.Remove(gone); err != nil {
		t.Fatal(err)
	}
	for n := 0; n < 2; n++ {
		if _, err := Create(root, n); !errors.Is(err, errSlotHeld) {
			t.Fatalf("Create over the junction %s must refuse as for a name already held, got: %v", SlotName(n), err)
		}
		if _, err := os.Lstat(filepath.Join(root, SlotName(n))); err != nil {
			t.Fatalf("the junction %s must stay as it was: %v", SlotName(n), err)
		}
	}
	if entries, err := os.ReadDir(present); err != nil || len(entries) != 0 {
		t.Fatalf("nothing may be written through the junction: %v, %v", entries, err)
	}
	if _, err := os.Lstat(gone); !os.IsNotExist(err) {
		t.Fatalf("nothing may be made where the junction points: %v", err)
	}
}
