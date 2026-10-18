//go:build !windows

package fileperm

import (
	"os"
	"path/filepath"
	"testing"
)

// On unix a slot root is 0700 wherever it is made, and the reader tells a
// directory others may enter from one they may not.
func TestAnOwnerOnlyDirIs0700(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "identity-0")
	if err := MkdirOwnerOnly(dir); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := fi.Mode().Perm(); got != 0o700 {
		t.Fatalf("slot root mode = %#o, want 0700", got)
	}

	// Control half: a directory open to group and other is not owner-only.
	open := filepath.Join(t.TempDir(), "open")
	if err := os.Mkdir(open, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(open, 0o755); err != nil {
		t.Fatal(err)
	}
	if ok, err := IsOwnerOnlyDir(open); err != nil || ok {
		t.Fatalf("IsOwnerOnlyDir(0755 dir) = %v, %v; want false", ok, err)
	}
}
