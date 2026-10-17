//go:build linux || android

package atomicfile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAFileWithNoOwnerReadIsMadeWritableThroughProc(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root opens a file at mode 0000 to write: nothing is made writable")
	}
	dir := t.TempDir()
	token, other := filepath.Join(dir, "token.json"), filepath.Join(dir, "other")
	if err := os.WriteFile(token, []byte("TOKEN"), 0o000); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(token, other); err != nil {
		t.Skipf("hard links unavailable here: %v", err)
	}
	if err := Remove(token); err != nil {
		t.Fatalf("Remove of a file at 0000 with another name: %v", err)
	}
	if info, err := os.Lstat(other); err != nil {
		t.Errorf("the other name stands: %v", err)
	} else if info.Size() != 0 {
		t.Errorf("the other name holds nothing of the file: it holds %d bytes", info.Size())
	}
}

// WITHOUT /PROC THE REFUSAL SAYS HOW TO FIND THE OTHER NAME. Where the
// system has no /proc (a chroot), the file cannot be made writable, and
// the removal is refused, naming find -samefile. The seam stands such a
// system in. Skipped as root.
func TestWithoutProcTheRefusalSaysHowToFindTheOtherName(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root opens a file at mode 0000 to write: nothing is made writable")
	}
	procSelfFD = filepath.Join(t.TempDir(), "no-proc") + "/"
	t.Cleanup(func() { procSelfFD = "/proc/self/fd/" })
	dir := t.TempDir()
	token, other := filepath.Join(dir, "token.json"), filepath.Join(dir, "other")
	if err := os.WriteFile(token, []byte("TOKEN"), 0o000); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(token, other); err != nil {
		t.Skipf("hard links unavailable here: %v", err)
	}
	err := Remove(token)
	if err == nil || !strings.Contains(err.Error(), "-samefile "+token) {
		t.Errorf("without /proc the removal is refused, naming find -samefile: %v", err)
	}
	if _, serr := os.Lstat(token); serr != nil {
		t.Errorf("a file that could not be emptied is not removed: %v", serr)
	}
}
