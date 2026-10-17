//go:build windows

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A SLOT BEHIND A JUNCTION TO NOTHING IS NOT STARTED, AND THE PERSON IS
// TOLD WHERE IT POINTS. This is the launch a person makes with the drive
// their identity lives on not connected — from the Start menu, or Windows'
// own at sign-in with --dir. Nothing is started, no page is opened and no
// log directory is made; a folder named with --dir that is simply not
// there is named alone.
func TestLauncherDoesNotStartASlotItCannotReach(t *testing.T) {
	root := t.TempDir()
	gone := filepath.Join(t.TempDir(), "gone")
	if err := os.Mkdir(gone, 0o700); err != nil {
		t.Fatal(err)
	}
	slot := filepath.Join(root, "identity-0")
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", slot, gone).CombinedOutput(); err != nil {
		t.Fatalf("mklink /J %s %s: %v: %s", slot, gone, err, out)
	}
	if err := os.Remove(gone); err != nil {
		t.Fatal(err)
	}
	var opened []string
	openURL := func(url string) error { opened = append(opened, url); return nil }
	notStarted := filepath.Join(root, "not-a-real-daemon.exe")

	for _, startup := range []bool{false, true} {
		err := runSlot(notStarted, slot, 0, startup, openURL, func(int) bool { return false })
		if err == nil {
			t.Fatalf("startup=%v: a slot behind a junction to nothing was launched", startup)
		}
		said := err.Error()
		for _, want := range []string{"Your identity's folder " + slot + " points at ", filepath.Base(gone) + ", which cannot be reached.", "Nothing was changed."} {
			if !strings.Contains(said, want) {
				t.Fatalf("startup=%v: the refusal lacks %q: %s", startup, want, said)
			}
		}
	}
	absent := filepath.Join(root, "identity-1")
	want := "Your identity's folder " + absent + " cannot be reached. Restore that folder, then open AII OS again. Nothing was changed."
	if err := runSlot(notStarted, absent, 1, true, openURL, func(int) bool { return false }); err == nil || err.Error() != want {
		t.Fatalf("a folder that is not there said:\n  %v\nwant:\n  %s", err, want)
	}
	if len(opened) != 0 {
		t.Fatalf("a page was opened for a slot that cannot be reached: %v", opened)
	}
	if _, err := os.Lstat(absent); !os.IsNotExist(err) {
		t.Fatalf("the launcher made the folder it could not reach: %v", err)
	}
	if _, err := os.Lstat(gone); !os.IsNotExist(err) {
		t.Fatalf("the launcher made the junction's target: %v", err)
	}
}
