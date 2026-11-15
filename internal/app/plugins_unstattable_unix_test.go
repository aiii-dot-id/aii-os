//go:build !windows

package app

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// A PACKAGE THAT CANNOT BE LOOKED AT IS NOT AN ABSENCE (A5). The scan skipped
// a package whose stat failed, and the facility withdrew the plugin it served
// as if it had been removed. A package gone since the listing is still no
// package; one that cannot be looked at is carried, with its error, for the
// facility to treat as unreadable (pluginfacility.Found.Err), and the rest of
// the scan goes on: another plugin beside it is found as before. A link to
// itself is a stat that fails on every system this file builds for, root's
// included. It needs Found.Err, which the scan before A5 does not have.
func TestAPackageThatCannotBeLookedAtIsCarriedNotDropped(t *testing.T) {
	t.Chdir(t.TempDir())
	a := &App{}
	dir := filepath.Join("plugins", "one")
	other := filepath.Join("plugins", "two")
	for _, d := range []string{dir, other} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(other, "two.aiiospkg"), []byte("p"), 0o600); err != nil {
		t.Fatal(err)
	}
	pkg := filepath.Join(dir, "one.aiiospkg")

	// Gone between the listing and the look: a link to nothing is no package.
	if err := os.Symlink("nothing-here", pkg); err != nil {
		t.Fatal(err)
	}
	d := a.discoverPlugins()
	if d.Err != nil || len(d.Found) != 1 || d.Found[0].Dir != other || d.Found[0].Err != nil {
		t.Fatalf("a package that is not there is no package, and the one beside it is found: %+v", d)
	}

	// There, and it cannot be looked at.
	if err := os.Remove(pkg); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("one.aiiospkg", pkg); err != nil {
		t.Fatal(err)
	}
	d = a.discoverPlugins()
	if d.Err != nil {
		t.Fatalf("one package that cannot be looked at failed the whole scan: %v", d.Err)
	}
	var carried, found bool
	for _, fd := range d.Found {
		var pathErr *fs.PathError
		switch fd.Dir {
		case dir:
			carried = fd.Package == pkg && errors.As(fd.Err, &pathErr)
		case other:
			found = fd.Err == nil && fd.Size == 1
		}
	}
	if !carried {
		t.Fatalf("A PACKAGE THAT COULD NOT BE LOOKED AT WAS AN ABSENCE: the plugin it serves is withdrawn on it: %+v", d)
	}
	if !found {
		t.Fatalf("the plugin beside it was not found as before: %+v", d)
	}
}
