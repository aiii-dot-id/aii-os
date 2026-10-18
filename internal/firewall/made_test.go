package firewall

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestWhatACallMadeThatCannotBeReadCountsAsKept(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a directory's mode does not close it on Windows")
	}
	if os.Getuid() == 0 {
		t.Skip("root reads a directory at mode 000: nothing here fails to be read")
	}
	made := filepath.Join(t.TempDir(), "made")
	closed := filepath.Join(made, "closed")
	if err := os.MkdirAll(closed, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(closed, "inside.json"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(closed, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(closed, 0o700) })
	if _, err := os.ReadDir(closed); err == nil {
		t.Skip("a directory at mode 000 is still read here: nothing fails to be read")
	}
	var asked []string
	nothingKept := func(path string) error { asked = append(asked, path); return nil }
	if err := MadeKept(nothingKept, made, true, false); err == nil {
		t.Errorf("an entry of what a call made that cannot be read is passed over, not counted as kept (asked %v)", asked)
	}
	if err := os.Chmod(closed, 0o700); err != nil {
		t.Fatal(err)
	}
	asked = nil
	if err := MadeKept(nothingKept, made, true, false); err != nil || len(asked) != 3 {
		t.Errorf("once it can be read, every entry is asked and nothing is kept: %v, asked %v", err, asked)
	}
}

func TestWhatACallMadeThatIsGoneIsNotKept(t *testing.T) {
	gone := filepath.Join(t.TempDir(), "made")
	if err := MadeKept(func(string) error { return nil }, gone, true, false); err != nil {
		t.Errorf("a made entry that is gone counts as kept: %v", err)
	}
}

// racingDirs is the process's file system with another actor in it: just
// before the call's own Mkdir of other, the other actor makes it.
type racingDirs struct{ other string }

func (r racingDirs) Lstat(name string) (fs.FileInfo, error) { return os.Lstat(name) }
func (r racingDirs) Mkdir(name string, perm fs.FileMode) error {
	if name == r.other {
		os.Mkdir(r.other, 0o700)
	}
	return os.Mkdir(name, perm)
}
func (r racingDirs) Remove(name string) error { return os.Remove(name) }

func TestMakeParentsCountsOnlyTheDirectoriesItMade(t *testing.T) {
	home := t.TempDir()
	other := filepath.Join(home, "theirs")
	path := filepath.Join(other, "sub", "x.txt")
	made, err := MakeParents(racingDirs{other}, path, 0o755)
	if err != nil || len(made) != 1 || made[0] != filepath.Join(other, "sub") {
		t.Fatalf("the call made only %s, and says so: %v %v", filepath.Join(other, "sub"), made, err)
	}
	if err := Unmake(Local, made); err != nil {
		t.Fatalf("taking back what it made: %v", err)
	}
	if _, err := os.Lstat(other); err != nil {
		t.Errorf("the other actor's directory was taken back: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(other, "sub")); err == nil {
		t.Error("the directory the call made was not taken back")
	}
}
