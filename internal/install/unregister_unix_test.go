//go:build (darwin && !ios) || (linux && !android)

package install

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestUnregisteringTwiceChangesNothingTheSecondTime(t *testing.T) {
	home := ownHome(t)
	m := newFakeManager(t, home)
	root := filepath.Join(home, Dir)
	slot := SlotName(90)
	if err := os.MkdirAll(filepath.Join(root, slot), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Register(slot); err != nil {
		t.Fatalf("register: %v", err)
	}
	link := filepath.Join(home, Dir, MachineDirName, "link")
	if err := RegisterMachine("/opt/aii/aii", link); err != nil {
		t.Fatalf("register the machine service: %v", err)
	}
	if SlotGone(slot) == nil || MachineGone() == nil {
		t.Fatal("rig: a registered slot and service read as gone")
	}
	for round := 1; round <= 2; round++ {
		if err := Unregister(slot); err != nil {
			t.Fatalf("unregister, round %d: %v", round, err)
		}
		if err := UnregisterMachine(); err != nil {
			t.Fatalf("unregister the machine service, round %d: %v", round, err)
		}
		if err := SlotGone(slot); err != nil {
			t.Fatalf("round %d: the slot is not gone: %v", round, err)
		}
		if err := MachineGone(); err != nil {
			t.Fatalf("round %d: the machine service is not gone: %v", round, err)
		}
		snap := m.snapshot()
		if round == 1 {
			m.first = snap
		} else if !slices.Equal(snap, m.first) {
			t.Fatalf("the second unregister changed what is registered:\n first  %q\n second %q", m.first, snap)
		}
	}
	if _, err := os.Stat(filepath.Join(root, slot)); err != nil {
		t.Fatalf("unregistering touched the slot: %v", err)
	}
}

func TestUnregisterRefusesUnderSudoAndInAForeignHome(t *testing.T) {
	home := ownHome(t)
	m := newFakeManager(t, home)
	slot := SlotName(90)
	if err := os.MkdirAll(filepath.Join(home, Dir, slot), 0o700); err != nil {
		t.Fatal(err)
	}
	acts := map[string]func() error{
		"register":                  func() error { _, _, err := Register(slot); return err },
		"unregister":                func() error { return Unregister(slot) },
		"stop":                      func() error { return Stop(slot) },
		"register the machine":      func() error { return RegisterMachine("/opt/aii/aii", filepath.Join(home, "link")) },
		"unregister the machine":    UnregisterMachine,
		"the account's own (check)": OwnAccount,
	}
	sandbox := filepath.Join(home, Dir, slot, "sandbox")
	if err := os.MkdirAll(sandbox, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", sandbox)
	for name, act := range acts {
		var foreign *ForeignAccountError
		if err := act(); !errors.As(err, &foreign) || foreign.Home != sandbox {
			t.Errorf("%s under a foreign HOME: %v, want a ForeignAccountError naming %s", name, err, sandbox)
		}
	}
	t.Setenv("HOME", home)
	other := someOtherAccount(t)
	t.Setenv("SUDO_USER", other.Username)
	for name, act := range acts {
		var foreign *ForeignAccountError
		if err := act(); !errors.As(err, &foreign) || foreign.Sudo != other.Username {
			t.Errorf("%s under sudo for %s: %v, want a ForeignAccountError naming the sudo", name, other.Username, err)
		}
	}
	if got := m.calls(); len(got) != 0 {
		t.Fatalf("a refused act asked the manager: %q", got)
	}
	t.Setenv("SUDO_USER", "")
	if err := OwnAccount(); err != nil {
		t.Fatalf("the account's own home, unelevated: %v", err)
	}
}

// A SLOT ON ANOTHER VOLUME, REACHED THROUGH A LINK, IS ONE SLOT EVERYWHERE:
// listed by Slots and SlotDirs (X1: a link's DirEntry is not a directory,
// and Slots missed it) and named by the startup registrations alike.
func TestStartupSlotsAndSlotDirsAgreeOnASymlinkedSlot(t *testing.T) {
	home := ownHome(t)
	newFakeManager(t, home)
	root := filepath.Join(home, Dir)
	volume := filepath.Join(t.TempDir(), "slot")
	if err := os.MkdirAll(volume, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	linked := filepath.Join(root, SlotName(3))
	if err := os.Symlink(volume, linked); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Register(SlotName(3)); err != nil {
		t.Fatal(err)
	}
	dirs, err := SlotDirs(root)
	if err != nil {
		t.Fatal(err)
	}
	ns, err := Slots(root)
	if err != nil {
		t.Fatal(err)
	}
	startup, err := StartupSlots()
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(dirs, []string{linked}) || !slices.Equal(ns, []int{3}) || len(startup) != 1 || startup[0].Dir != linked {
		t.Fatalf("SlotDirs %v, Slots %v and StartupSlots %+v disagree on the linked slot %s", dirs, ns, startup, linked)
	}
}
