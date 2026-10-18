//go:build !windows

package atomicfile

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// noSwap stands a system that makes no swap in for exchangeNames.
func noSwap(t *testing.T) {
	t.Helper()
	exchangeNames = func(string, string) error { return syscall.ENOTSUP }
	t.Cleanup(func() { exchangeNames = exchange })
}

// swaps reports whether this system and file system swap two names in one
// step, asked of two files in dir.
func swaps(t *testing.T, dir string) bool {
	t.Helper()
	a, b := filepath.Join(dir, ".swap-a"), filepath.Join(dir, ".swap-b")
	for _, p := range []string{a, b} {
		if err := os.WriteFile(p, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		defer os.Remove(p)
	}
	return exchange(a, b) == nil
}

func TestAWriterThatRenamesInTheMomentDoesNotHaveItsFileEmptied(t *testing.T) {
	dir := t.TempDir()
	if !swaps(t, dir) {
		t.Skip("this system or file system makes no swap: the replace renames, with this moment (the KNOWN_LIMITATIONS row)")
	}
	target := filepath.Join(dir, "config.json")
	backup := target + "~"
	original, displaced := filepath.Join(dir, "original-name"), filepath.Join(dir, "displaced-name")
	if err := os.WriteFile(target, []byte("THE ORIGINAL"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(target, original); err != nil {
		t.Skipf("hard links unavailable here: %v", err)
	}
	replaceWindow = func(at string) {
		if err := os.Rename(at, backup); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(at, []byte("THE EDITOR'S SAVE"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Link(at, displaced); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { replaceWindow = nil })
	if published, err := WriteReplace(target, []byte("THE HOST'S SAVE"), 0o600); err != nil || !published {
		t.Fatalf("the host's save: published=%v err=%v", published, err)
	}
	for path, want := range map[string]string{
		target:    "THE HOST'S SAVE",
		backup:    "THE ORIGINAL",
		original:  "THE ORIGINAL",
		displaced: "",
	} {
		if got, err := os.ReadFile(path); err != nil || string(got) != want {
			t.Errorf("%s holds %q (%v), want %q", filepath.Base(path), got, err, want)
		}
	}
}

// WHERE NO SWAP IS MADE, A SAVE STILL EMPTIES WHAT IT REPLACES. The
// replace renames, as before the swap (openEarlier, earlier.empty), and a
// read-only file with another name is made writable by its owner there
// too. Run with the swap refused (ENOTSUP), as on a file system without it.
func TestWhereNoSwapIsMadeASaveStillEmptiesWhatItReplaces(t *testing.T) {
	noSwap(t)
	dir := t.TempDir()
	for _, mode := range []os.FileMode{0o600, 0o400} {
		target := filepath.Join(dir, "config.json")
		other := filepath.Join(dir, "elsewhere.txt")
		os.Remove(other)
		os.Remove(target)
		if err := os.WriteFile(target, []byte("THE EARLIER SECRET"), mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Link(target, other); err != nil {
			t.Skipf("hard links unavailable here: %v", err)
		}
		if published, err := WriteReplace(target, []byte("new"), 0o600); err != nil || !published {
			t.Fatalf("mode %o: the save lands without a swap: published=%v err=%v", mode, published, err)
		}
		if got, err := os.ReadFile(other); err != nil || len(got) != 0 {
			t.Errorf("mode %o: without a swap the other name holds nothing of the replaced file: %q %v", mode, got, err)
		}
		os.Chmod(other, 0o600)
	}
}

// syncsSeen counts the directory syncs a replace makes and records, at
// each, whether name still stands.
type syncsSeen struct {
	name  string
	stood []bool
	fail  int // the sync, counted from 1, that fails; 0 for none
}

func (s *syncsSeen) sync() error {
	_, err := os.Lstat(s.name)
	s.stood = append(s.stood, err == nil)
	if len(s.stood) == s.fail {
		return syscall.EIO
	}
	return nil
}

func TestASwapIsPublishedOnlyOnceTheStagedNameIsGone(t *testing.T) {
	dir := t.TempDir()
	if !swaps(t, dir) {
		t.Skip("this system or file system makes no swap: a staged file is renamed")
	}
	staged, target := filepath.Join(dir, ".config.json.tmp-1"), filepath.Join(dir, "config.json")
	for _, fail := range []int{0, 2} {
		if err := os.WriteFile(staged, []byte("new"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, []byte("earlier"), 0o600); err != nil {
			t.Fatal(err)
		}
		seen := &syncsSeen{name: staged, fail: fail}
		published, err := replaceStagedWith(staged, target, seen.sync)
		if fail == 0 && (err != nil || !published) {
			t.Fatalf("the swap: published=%v err=%v", published, err)
		}
		if fail == 2 && (!published || err == nil) {
			t.Errorf("a sync after the staged name's removal that fails is an error beside the publication: published=%v err=%v", published, err)
		}
		if len(seen.stood) < 2 || seen.stood[len(seen.stood)-1] {
			t.Errorf("at the last directory sync the staged name is gone: syncs %v (true: the staged name stood)", seen.stood)
		}
		if got, _ := os.ReadFile(target); string(got) != "new" {
			t.Errorf("the name holds the new file: %q", got)
		}
	}
}

func TestANamedFileIsMovedByARenameNeverASwap(t *testing.T) {
	exchangeNames = func(oldPath, newPath string) error {
		t.Errorf("%s was swapped with %s: a named file is renamed", oldPath, newPath)
		return exchange(oldPath, newPath)
	}
	t.Cleanup(func() { exchangeNames = exchange })
	dir := t.TempDir()
	marker, retired := filepath.Join(dir, ".boot_completed"), filepath.Join(dir, ".boot_completed.retired")
	other := filepath.Join(dir, "elsewhere")
	if err := os.WriteFile(marker, []byte("MARKER"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(retired, []byte("EARLIER RETIRED"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(retired, other); err != nil {
		t.Skipf("hard links unavailable here: %v", err)
	}
	seen := &syncsSeen{name: marker}
	if published, err := replace(marker, retired, seen.sync); err != nil || !published {
		t.Fatalf("the move: published=%v err=%v", published, err)
	}
	if len(seen.stood) == 0 || seen.stood[len(seen.stood)-1] {
		t.Errorf("at the last directory sync the source is gone: syncs %v (true: it stood)", seen.stood)
	}
	if got, _ := os.ReadFile(retired); string(got) != "MARKER" {
		t.Errorf("the target holds the moved file: %q", got)
	}
	if got, _ := os.ReadFile(other); len(got) != 0 {
		t.Errorf("the file the move displaced is emptied under its other name: %q", got)
	}
	if err := os.WriteFile(marker, []byte("MARKER AGAIN"), 0o644); err != nil {
		t.Fatal(err)
	}
	if published, err := Replace(marker, retired); err != nil || !published {
		t.Fatalf("Replace of a named file: published=%v err=%v", published, err)
	}
	if _, err := os.Lstat(marker); err == nil {
		t.Error("Replace of a named file leaves no source standing")
	}
}

func TestTheFileOpenedAfterItWasMadeWritableIsTheOneJudged(t *testing.T) {
	dir := t.TempDir()
	token, other := filepath.Join(dir, "token.json"), filepath.Join(dir, "other")
	innocent, innocentToo := filepath.Join(dir, "innocent-a"), filepath.Join(dir, "innocent-b")
	if err := os.WriteFile(token, []byte("TOKEN"), 0o400); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(innocent, []byte("INNOCENT"), 0o600); err != nil {
		t.Fatal(err)
	}
	for from, to := range map[string]string{token: other, innocent: innocentToo} {
		if err := os.Link(from, to); err != nil {
			t.Skipf("hard links unavailable here: %v", err)
		}
	}
	if os.Getuid() == 0 {
		t.Skip("root opens a file at mode 0400 to write: nothing is made writable")
	}
	ownerWindow = func(path string) {
		if err := os.Rename(innocent, path); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { ownerWindow = nil })
	if err := Remove(token); err == nil || !strings.Contains(err.Error(), "changed while it was made writable") {
		t.Errorf("a file put at the path while it was made writable is refused: %v", err)
	}
	if got, err := os.ReadFile(innocentToo); err != nil || string(got) != "INNOCENT" {
		t.Errorf("the other actor's file is not emptied: %q %v", got, err)
	}
}

func TestASwapThatFailsSaysWhichNames(t *testing.T) {
	exchangeNames = func(string, string) error { return syscall.EIO }
	t.Cleanup(func() { exchangeNames = exchange })
	dir := t.TempDir()
	target := filepath.Join(dir, "config.json")
	if err := os.WriteFile(target, []byte("earlier"), 0o600); err != nil {
		t.Fatal(err)
	}
	published, err := WriteReplace(target, []byte("new"), 0o600)
	if published || err == nil || !strings.Contains(err.Error(), "swap") || !strings.Contains(err.Error(), target) {
		t.Errorf("a failed swap is not published and names the name it was for: published=%v err=%v", published, err)
	}
	if got, _ := os.ReadFile(target); string(got) != "earlier" {
		t.Errorf("a failed swap leaves the name as it was: %q", got)
	}
}
