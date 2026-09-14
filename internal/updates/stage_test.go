package updates

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// .
// .
// .
// .
func TestCanStageBesideRefusesAnUnwritableDirectory(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can write anywhere; this test needs an unprivileged user")
	}
	dir := t.TempDir()
	exe := filepath.Join(dir, "aii")
	if err := os.WriteFile(exe, []byte("#!/bin/true\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
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

// .
// .
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

// .
// .
// .
// .
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
