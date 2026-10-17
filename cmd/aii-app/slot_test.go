//go:build (darwin && !ios) || (linux && !android)

package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/aiii-dot-id/aii-os/internal/install"
)

// A SLOT THAT IS THERE BUT CANNOT BE REACHED STOPS THE LAUNCH, AND NOTHING
// IS MADE IN ITS PLACE. The slot is a link to a drive that is not
// connected. The launcher used to find no slot (or, finding one, go on to
// register it) and the person met a failure in the system's words; a slot
// made in its place would have been a second identity where they expected
// their own. They are told which folder, where it points and what to do;
// aii is asked nothing; the link is as it was.
func TestALaunchWhoseSlotCannotBeReachedSaysSoAndChangesNothing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SUDO_USER", "")
	root := filepath.Join(home, install.Dir)
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "not-connected", install.SlotName(0))
	link := filepath.Join(root, install.SlotName(0))
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	calls := filepath.Join(home, "aii-calls")
	aii := filepath.Join(home, "aii")
	if err := os.WriteFile(aii, []byte("#!/bin/sh\necho \"$@\" >> '"+calls+"'\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	err := launch(aii)
	want := "Your identity's folder " + link + " points at " + target + ", which cannot be reached. Connect that drive or restore that folder, then open AII OS again. Nothing was changed."
	if err == nil || err.Error() != want {
		t.Fatalf("the launch said:\n  %v\nwant:\n  %s", err, want)
	}
	if raw, _ := os.ReadFile(calls); len(raw) != 0 {
		t.Fatalf("nothing was to be changed, and aii was asked %q", raw)
	}
	if ns, err := install.Slots(root); err != nil || !slices.Equal(ns, []int{0}) {
		t.Fatalf("the account's slots are %v, %v; want the one it had", ns, err)
	}
	if got, err := os.Readlink(link); err != nil || got != target {
		t.Fatalf("the link must stay as it was: Readlink = %q, %v; want %q", got, err, target)
	}
	if _, err := os.Lstat(target); !os.IsNotExist(err) {
		t.Fatalf("nothing may be made where the link points: %v", err)
	}
}

// reachable, kind by kind: a folder and a link to one can be entered; a
// link to nothing is named with where it points, from the link's own
// place when it says so relatively; what is no link and no folder is named
// alone, since there is no drive to connect.
func TestReachableNamesTheFolderAndWhereALinkPoints(t *testing.T) {
	root := t.TempDir()
	at := func(name string) string { return filepath.Join(root, name) }
	if err := os.Mkdir(at("folder"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, err := range []error{
		os.Symlink(at("folder"), at("linked")),
		os.Symlink(filepath.Join("..", "elsewhere", "slot"), at("relative")),
		unix.Mkfifo(at("fifo"), 0o600),
		os.WriteFile(at("file"), nil, 0o600),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"folder", "linked"} {
		if err := reachable(at(name)); err != nil {
			t.Fatalf("%s can be entered: %v", name, err)
		}
	}
	elsewhere := filepath.Join(filepath.Dir(root), "elsewhere", "slot")
	if err := reachable(at("relative")); err == nil || !strings.Contains(err.Error(), at("relative")+" points at "+elsewhere+", which cannot be reached.") {
		t.Fatalf("a relative link to nothing must be named with the place it points at (%s), got: %v", elsewhere, err)
	}
	for _, name := range []string{"fifo", "file", "absent"} {
		want := "Your identity's folder " + at(name) + " cannot be reached. Restore that folder, then open AII OS again. Nothing was changed."
		if err := reachable(at(name)); err == nil || err.Error() != want {
			t.Fatalf("%s said:\n  %v\nwant:\n  %s", name, err, want)
		}
	}
}
