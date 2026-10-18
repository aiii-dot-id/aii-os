//go:build (linux && !android) || (darwin && !ios) || windows

package plugstore

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestOpenForRemovalMakesNothingAndRefusesWhatIsNotAStoreHere(t *testing.T) {
	root := t.TempDir()
	gone := filepath.Join(root, "plugins")
	if _, err := OpenForRemoval(gone); !errors.Is(err, ErrStoreGone) {
		t.Fatalf("an absent store under a present parent: %v, want ErrStoreGone", err)
	}
	far := filepath.Join(root, "volume", "plugins")
	if _, err := OpenForRemoval(far); !errors.Is(err, ErrStoreUnreachable) {
		t.Fatalf("a store whose parent is missing: %v, want ErrStoreUnreachable", err)
	}
	for _, p := range []string{gone, far, filepath.Dir(far)} {
		if _, err := os.Lstat(p); !os.IsNotExist(err) {
			t.Fatalf("opening for removal made %s: %v", p, err)
		}
	}
	w := openWriter(t)
	if _, err := OpenForRemoval(w.Dir); !errors.Is(err, ErrHeld) {
		t.Fatalf("a store its writer holds: %v, want ErrHeld", err)
	}
	if runtime.GOOS != "windows" {
		link := filepath.Join(root, "linked")
		if err := os.Symlink(w.Dir, link); err != nil {
			t.Fatal(err)
		}
		if _, err := OpenForRemoval(link); err == nil {
			t.Fatal("a store reached through a link at its top was opened for removal")
		}
	}
	w.Close()
	r, err := OpenForRemoval(w.Dir)
	if err != nil {
		t.Fatalf("a store nothing holds: %v", err)
	}
	defer r.Close()
	if _, err := OpenWriter(w.Dir); !errors.Is(err, ErrHeld) {
		t.Fatalf("a writer while the store is open for removal: %v, want ErrHeld", err)
	}
	if left, _ := os.ReadDir(filepath.Join(w.Dir, DirBlobs)); len(left) != 0 {
		t.Fatalf("opening for removal probed the store: %v", left)
	}
}
