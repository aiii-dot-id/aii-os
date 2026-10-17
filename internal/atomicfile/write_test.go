package atomicfile

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/fileperm"
)

// The whole-file writers are the doors fourteen copies of the same
// sequence came through: unique temporary beside the target, the mode,
// write, sync, close, publish. What these tests hold them to is what the
// copies each got wrong at least once — content published whole, the
// temporary never left behind, and a secret's mode meaning owner-only on
// every platform.

// onlyEntry fails unless dir holds exactly the one name: a leftover
// temporary is the shape a later reader mistakes for a crash.
func onlyEntry(t *testing.T, dir, name string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if len(names) != 1 || names[0] != name {
		t.Fatalf("directory holds %q, want only %q", names, name)
	}
}

func TestWriteReplacePublishesWholeAndLeavesNoTemporary(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}

	published, err := WriteReplace(path, []byte("new"), 0o600)
	if err != nil || !published {
		t.Fatalf("WriteReplace: published=%v err=%v", published, err)
	}
	if got, _ := os.ReadFile(path); string(got) != "new" {
		t.Fatalf("content = %q, want new", got)
	}
	onlyEntry(t, dir, "config.json")
}

// A publish that cannot happen leaves the old content where it was and
// takes its temporary with it. The target here is a directory, which no
// rename may replace with a file.
func TestWriteReplaceThatCannotPublishLeavesTheOldAndNoTemporary(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "inside"), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}

	published, err := WriteReplace(path, []byte("new"), 0o600)
	if err == nil || published {
		t.Fatalf("WriteReplace over a directory: published=%v err=%v, want a refusal that published nothing", published, err)
	}
	if got, _ := os.ReadFile(filepath.Join(path, "inside")); string(got) != "old" {
		t.Fatalf("the old content was disturbed: %q", got)
	}
	onlyEntry(t, dir, "state")
}

func TestWriteNewRefusesAnExistingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "identity.key")

	published, err := WriteNew(path, []byte("first"), 0o600)
	if err != nil || !published {
		t.Fatalf("WriteNew on a fresh name: published=%v err=%v", published, err)
	}
	published, err = WriteNew(path, []byte("second"), 0o600)
	if err == nil || published {
		t.Fatalf("WriteNew over an existing file: published=%v err=%v, want a refusal that published nothing", published, err)
	}
	if got, _ := os.ReadFile(path); string(got) != "first" {
		t.Fatalf("the existing file was disturbed: %q", got)
	}
	onlyEntry(t, dir, "identity.key")
}

// A mode with no group or other bits is a request for secrecy, and on
// Windows only the DACL can grant it; any other mode is a plain mode.
// The mode is the one asked for, not the one a umask or a stale file
// leaves.
func TestWriteGivesTheModeAndRestrictsASecretToItsOwner(t *testing.T) {
	for _, tc := range []struct {
		perm   os.FileMode
		secret bool
	}{
		{0o600, true},
		{0o644, false},
		{0o755, false},
	} {
		for _, write := range []struct {
			name string
			fn   func(string, []byte, os.FileMode) (bool, error)
		}{{"WriteReplace", WriteReplace}, {"WriteNew", WriteNew}} {
			path := filepath.Join(t.TempDir(), "f")
			if published, err := write.fn(path, []byte("x"), tc.perm); err != nil || !published {
				t.Fatalf("%s %o: published=%v err=%v", write.name, tc.perm, published, err)
			}
			restricted, err := fileperm.IsRestrictedToOwner(path)
			if err != nil {
				t.Fatal(err)
			}
			if restricted != tc.secret {
				t.Errorf("%s %o: restricted to owner = %v, want %v", write.name, tc.perm, restricted, tc.secret)
			}
			if runtime.GOOS == "windows" {
				continue // the mode bits are not the protection there
			}
			fi, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if fi.Mode().Perm() != tc.perm {
				t.Errorf("%s %o: mode = %o", write.name, tc.perm, fi.Mode().Perm())
			}
		}
	}
}
