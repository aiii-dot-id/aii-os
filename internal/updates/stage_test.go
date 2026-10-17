package updates

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// A package-managed install (/usr/bin/aii, root-owned) cannot stage a
// new image beside the running one. Detecting that is what keeps the
// checker from downloading a release every hour and failing at the
// last step, reporting a healthy arrangement as a fault.
func TestCanStageBesideRefusesAnUnwritableDirectory(t *testing.T) {
	if ranUnprivileged(t) {
		return // root can write anywhere: it ran as an unprivileged user
	}
	dir := t.TempDir()
	exe := filepath.Join(dir, "aii")
	if err := os.WriteFile(exe, []byte("#!/bin/true\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		// A directory's writability on Windows is a DACL, not a mode
		// bit: os.Mkdir(0o500) and os.Chmod both move only the
		// read-only ATTRIBUTE, which does not stop file creation. The
		// precondition this test needs therefore never exists, and the
		// probe correctly reports the directory as writable — it
		// attempts a real write rather than trusting a mode. Expressing
		// this properly needs a deny-write ACE, which risks leaving an
		// undeletable TempDir behind.
		t.Skip("a mode bit cannot make a directory unwritable on Windows")
	}
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o755) })

	ok, why := canStageBesideAt(exe)
	if ok {
		t.Fatal("staging reported possible in a read-only directory")
	}
	if why == "" {
		t.Fatal("refusal carried no reason — absence must be reported in words an operator can act on")
	}
	if !strings.Contains(why, exe) {
		t.Fatalf("reason must name the binary an operator has to update: %q", why)
	}
}

// The normal case: a user-writable install can stage, and the probe
// leaves nothing behind.
func TestCanStageBesideAllowsAWritableDirectoryAndLeavesNoLitter(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "aii")
	if err := os.WriteFile(exe, []byte("#!/bin/true\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	ok, why := canStageBesideAt(exe)
	if !ok {
		t.Fatalf("staging refused in a writable directory: %s", why)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("probe left litter beside the binary: %v", names)
	}
}

// The chmod test above cannot run as root, and the gate runs as root,
// so this one exercises the same refusal path without needing an
// unprivileged user: a directory that does not exist cannot be staged
// into by anybody.
func TestCanStageBesideRefusesWhenTheDirectoryIsGone(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "vanished", "aii")
	ok, why := canStageBesideAt(exe)
	if ok {
		t.Fatal("staging reported possible into a directory that does not exist")
	}
	if !strings.Contains(why, exe) {
		t.Fatalf("reason must name the binary an operator has to update: %q", why)
	}
}
