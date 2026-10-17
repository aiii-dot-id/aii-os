package atomicfile

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestARemovedFileLeavesNothingUnderAnotherName(t *testing.T) {
	dir := t.TempDir()
	for _, mode := range []os.FileMode{0o600, 0o400, 0o000} {
		token := filepath.Join(dir, "token.json")
		other := filepath.Join(dir, "elsewhere.txt")
		if err := os.WriteFile(token, []byte(`{"refresh_token":"THE TOKEN"}`), mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Link(token, other); err != nil {
			t.Skipf("hard links unavailable here: %v", err)
		}
		if err := Remove(token); err != nil {
			t.Fatalf("mode %o: Remove of a file with another name: %v", mode, err)
		}
		if _, err := os.Lstat(token); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("mode %o: the name is removed: %v", mode, err)
		}
		if info, err := os.Lstat(other); err != nil || info.Size() != 0 {
			t.Errorf("mode %o: the other name holds nothing of the removed file: %v %v", mode, info, err)
		}
		os.Chmod(other, 0o600)
		os.Remove(other)
	}

	single := filepath.Join(dir, "single.json")
	if err := os.WriteFile(single, []byte("x"), 0o400); err != nil {
		t.Fatal(err)
	}
	if err := Remove(single); err != nil {
		t.Errorf("a file with one name is removed: %v", err)
	}
	if err := Remove(single); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("what is not there answers as os.Remove does: %v", err)
	}
	target := filepath.Join(dir, "kept-by-the-link")
	link := filepath.Join(dir, "link")
	if err := os.WriteFile(target, []byte("the target"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Logf("symbolic links unavailable here (%v): the link is not asked", err)
		return
	}
	if err := Remove(link); err != nil {
		t.Errorf("a link is removed: %v", err)
	}
	if got, err := os.ReadFile(target); err != nil || string(got) != "the target" {
		t.Errorf("what a removed link leads to is not touched: %q %v", got, err)
	}
}

func TestANameMadeJustBeforeTheRemovalHoldsNothingAfterIt(t *testing.T) {
	dir := t.TempDir()
	modes := []os.FileMode{0o600}
	if runtime.GOOS == "windows" {
		modes = append(modes, 0o400)
	}
	for _, mode := range modes {
		token, late := filepath.Join(dir, "token.json"), filepath.Join(dir, "late")
		os.Remove(late)
		if err := os.WriteFile(token, []byte("THE TOKEN"), mode); err != nil {
			t.Fatal(err)
		}
		linked := false
		removeWindow = func(path string) {
			if err := os.Link(path, late); err != nil {
				t.Skipf("hard links unavailable here: %v", err)
			}
			linked = true
		}
		t.Cleanup(func() { removeWindow = nil })
		if err := Remove(token); err != nil {
			t.Fatalf("mode %o: Remove: %v", mode, err)
		}
		if !linked {
			t.Errorf("mode %o: Remove holds the file across its unlink: the late name was never made", mode)
			continue
		}
		if info, err := os.Lstat(late); err != nil || info.Size() != 0 {
			t.Errorf("mode %o: a name made just before the removal holds nothing of the file: %v", mode, err)
			if err == nil {
				t.Errorf("mode %o: it holds %d bytes", mode, info.Size())
			}
		}
	}
	removeWindow = nil
	single := filepath.Join(dir, "single")
	if err := os.WriteFile(single, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Remove(single); err != nil {
		t.Errorf("a file with one name and no late name is removed: %v", err)
	}
}
