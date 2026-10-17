//go:build !windows

package genesislive

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func placeholder() *Artifacts {
	return &Artifacts{Ring0: []byte("r0"), BootstrapPacket: []byte("bp"), Token: "t"}
}

func TestTheCopyIsKeptForThisAccountAlone(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("AII_GENESIS_CACHE", "")
	t.Setenv("XDG_CACHE_HOME", cache)
	t.Setenv("HOME", cache)
	path := CachePath()
	if path == "" {
		t.Fatal("no place for the copy under a cache directory of this account's own")
	}
	dir := filepath.Dir(path)
	st, err := os.Lstat(dir)
	if err != nil || !st.IsDir() || st.Mode().Perm() != 0o700 {
		t.Fatalf("the copy's directory %s: %v %v, want a directory of mode 0700", dir, st, err)
	}
	writeCache(path, placeholder())
	fst, err := os.Lstat(path)
	if err != nil || !fst.Mode().IsRegular() || fst.Mode().Perm() != 0o600 {
		t.Fatalf("the copy %s: %v %v, want a regular file of mode 0600 (it holds the token the platform issued)", path, fst, err)
	}
	if a := readCache(path); a == nil || a.Token != "t" {
		t.Fatalf("this account's own copy is not read back: %+v", a)
	}
	// A directory of this account's left open to others for reading by an
	// earlier build is closed, and still used.
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := CachePath(); got != path {
		t.Fatalf("a directory only this account can write is not used: %q", got)
	}
	if st, _ := os.Lstat(dir); st.Mode().Perm() != 0o700 {
		t.Fatalf("the directory was left open to others: %v", st.Mode().Perm())
	}
}

func TestACopyAnotherAccountCouldHaveWrittenIsPassedOver(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "good.json")
	writeCache(good, placeholder())
	if readCache(good) == nil {
		t.Fatal("rig: the control copy is not read")
	}

	// Writable by others: anyone could have changed it.
	loose := filepath.Join(dir, "loose.json")
	writeCache(loose, placeholder())
	if err := os.Chmod(loose, 0o666); err != nil {
		t.Fatal(err)
	}
	if readCache(loose) != nil {
		t.Error("a copy others can write was read")
	}

	// A link: what it names was not put here by this code.
	link := filepath.Join(dir, "link.json")
	if err := os.Symlink(good, link); err != nil {
		t.Skipf("symlinks unavailable here: %v", err)
	}
	if readCache(link) != nil {
		t.Error("a link was followed to a copy")
	}

	// Not a regular file.
	if err := os.Mkdir(filepath.Join(dir, "dir.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	if readCache(filepath.Join(dir, "dir.json")) != nil {
		t.Error("a directory was read as a copy")
	}
	fifo := filepath.Join(dir, "fifo.json")
	if err := syscall.Mkfifo(fifo, 0o600); err == nil {
		done := make(chan *Artifacts, 1)
		go func() { done <- readCache(fifo) }()
		select {
		case a := <-done:
			if a != nil {
				t.Error("a pipe was read as a copy")
			}
		case <-time.After(5 * time.Second):
			t.Error("reading a pipe as a copy held the caller")
		}
	}

	// Another account's file, where this one can make one (root only).
	if os.Geteuid() == 0 {
		theirs := filepath.Join(dir, "theirs.json")
		writeCache(theirs, placeholder())
		if err := os.Chown(theirs, 65534, -1); err != nil {
			t.Fatal(err)
		}
		if readCache(theirs) != nil {
			t.Error("another account's file was read as this account's copy")
		}
	}

	// Past its hour it is not the current copy, though it is still this
	// account's and is the fallback when the platform will not answer.
	old := filepath.Join(dir, "old.json")
	writeCache(old, placeholder())
	then := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(old, then, then); err != nil {
		t.Fatal(err)
	}
	if readCache(old) != nil || readCacheOlderThanTTL(old) == nil {
		t.Error("a copy two hours old must be the fallback and not the current copy")
	}
}

// NO COPY UNDER A KNOWN NAME IN THE SHARED TEMPORARY DIRECTORY. Without a
// cache directory of the user's, the copy went to
// <tmp>/aii-genesis-live-v1.json: a name anyone on the machine could
// take first. It goes under a directory this account owns, or nowhere.
func TestNoCopyUnderAKnownNameInTheSharedTemporaryDirectory(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("AII_GENESIS_CACHE", "")
	t.Setenv("XDG_CACHE_HOME", "")
	t.Setenv("HOME", "")
	t.Setenv("TMPDIR", tmp)
	path := CachePath()
	if path == filepath.Join(tmp, "aii-genesis-live-v1.json") {
		t.Fatalf("the copy is under a known name in the temporary directory itself: %s", path)
	}
	if path != "" {
		dir := filepath.Dir(path)
		if st, err := os.Lstat(dir); err != nil || !st.IsDir() || st.Mode().Perm() != 0o700 || filepath.Dir(dir) != tmp {
			t.Fatalf("the copy's directory %s: %v %v, want one of this account's own under %s", dir, st, err, tmp)
		}
	}
	// A directory of that name that others can write is not this
	// account's own: no copy is kept there.
	mine := filepath.Join(tmp, "aii-os-"+accountTag())
	if err := os.MkdirAll(mine, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(mine, 0o777); err != nil {
		t.Fatal(err)
	}
	if got := CachePath(); got != "" {
		t.Fatalf("a directory others can write was taken for the copy: %s", got)
	}
}
