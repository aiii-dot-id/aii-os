package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/install"
)

// .
// .
// .
// .

func tempHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	// .
	// .
	// .
	// .
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	// .
	// .
	t.Setenv("SUDO_USER", "")
	return home
}

// .
// .
func TestInitCreatesTheFirstSlot(t *testing.T) {
	home := tempHome(t)

	if code := runInit([]string{"--no-start"}); code != 0 {
		t.Fatalf("aii init exited %d", code)
	}
	slot := filepath.Join(home, install.Dir, install.SlotName(0))
	if _, err := os.Stat(slot); err != nil {
		t.Fatalf("the first slot was not created: %v", err)
	}
	// .
	if _, err := os.Stat(install.ConfigPathIn(slot)); err != nil {
		t.Fatalf("the slot carries no config: %v", err)
	}
}

// .
// .
func TestInitCreatesASecondSlotBesideTheFirst(t *testing.T) {
	home := tempHome(t)

	if code := runInit([]string{"--no-start"}); code != 0 {
		t.Fatalf("first init exited %d", code)
	}
	if code := runInit([]string{"--no-start"}); code != 0 {
		t.Fatalf("second init exited %d", code)
	}
	for _, n := range []int{0, 1} {
		if _, err := os.Stat(filepath.Join(home, install.Dir, install.SlotName(n))); err != nil {
			t.Fatalf("slot %d missing: %v", n, err)
		}
	}
}

// .
// .
func TestSlotsListsWhatInitCreated(t *testing.T) {
	tempHome(t)
	if code := runSlots(); code != 0 {
		t.Fatalf("aii slots exited %d on an empty home — nothing to list is not an error", code)
	}
	if code := runInit([]string{"--no-start"}); code != 0 {
		t.Fatalf("init exited %d", code)
	}
	if code := runSlots(); code != 0 {
		t.Fatalf("aii slots exited %d after a slot existed", code)
	}
}

// .
// .
func TestInitFailsWhenTheHomeCannotBeWritten(t *testing.T) {
	if runtime.GOOS == "windows" {
		// .
		// .
		// .
		// .
		// .
		// .
		t.Skip("chmod cannot express an unwritable directory on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("running as root: an unwritable directory is still writable")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("SUDO_USER", "")
	if err := os.Chmod(home, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(home, 0o700) })

	if code := runInit([]string{"--no-start"}); code == 0 {
		t.Fatal("aii init reported success into a home it cannot write")
	}
}
