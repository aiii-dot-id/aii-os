//go:build windows

package atomicfile

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// THE POSIX RENAME IS THE ONE MADE on the file system a release is tested
// on. Replace and Rename fall back to the call every earlier release made
// when it is absent, and every test of theirs would pass on that call
// except the ones with a reader holding the file; this says outright that
// the fallback is not what ran.
func TestThePosixRenameIsTheOneMadeHere(t *testing.T) {
	for name, flush := range map[string]func(windows.Handle) error{"no flush": nil, "flushed": windows.FlushFileBuffers} {
		dir := t.TempDir()
		from, to := filepath.Join(dir, "from"), filepath.Join(dir, "to")
		for _, p := range []string{from, to} {
			if err := os.WriteFile(p, []byte(p), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		taken, renamed, err := posixRename(from, to, flush)
		if !taken || !renamed || err != nil {
			t.Fatalf("%s: taken=%v renamed=%v err=%v; want the rename made here (is %s on a file system without POSIX rename semantics?)", name, taken, renamed, err, dir)
		}
		if got, err := os.ReadFile(to); err != nil || string(got) != from {
			t.Fatalf("%s: the destination holds %q, %v", name, got, err)
		}
		if _, err := os.Stat(from); !os.IsNotExist(err) {
			t.Fatalf("%s: the source still stands: %v", name, err)
		}
	}
}

// A NAME THAT IS NOT ABSOLUTE is the caller's working directory's, as it
// is for every other file call. A rename by handle would read a bare name
// as "beside the source".
func TestThePosixRenameTakesARelativeName(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")
	if err := os.Mkdir(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	from := filepath.Join(sub, "from")
	if err := os.WriteFile(from, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	if taken, renamed, err := posixRename(from, "to", nil); !taken || !renamed || err != nil {
		t.Fatalf("taken=%v renamed=%v err=%v", taken, renamed, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "to")); err != nil {
		t.Fatalf("the file is not in the working directory: %v", err)
	}
	if _, err := os.Stat(filepath.Join(sub, "to")); !os.IsNotExist(err) {
		t.Fatalf("the file went beside its source: %v", err)
	}
}

// A HANDLE THAT DOES NOT SHARE DELETE STILL HOLDS ITS WRITER OFF, and the
// writer says so after its budget: the old content stands, nothing is left
// beside it, and the error is the contention it is, by the package's own
// predicate. Go's os.Open is such a handle, which is why readers of a
// replaced file open it with Open. Let go, the writer gets through.
func TestAHandleThatDoesNotShareDeleteHoldsItsWriterOffForTheBudget(t *testing.T) {
	old, next := []byte("old"), []byte("new")
	path := filepath.Join(t.TempDir(), "state.json")
	if _, err := WriteReplace(path, old, 0o600); err != nil {
		t.Fatal(err)
	}
	held, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	published, err := WriteReplace(path, next, 0o600)
	waited := time.Since(start)
	if err == nil || published {
		held.Close()
		t.Fatalf("a replacement went through a handle that does not share delete (published=%v, err=%v): this test's premise is gone, and so is its reason", published, err)
	}
	if !renameContended(err) {
		held.Close()
		t.Fatalf("the refusal is not the contention it is: %v", err)
	}
	if waited < renameRetryBudget {
		held.Close()
		t.Fatalf("the writer gave up after %v, before its budget of %v", waited, renameRetryBudget)
	}
	if got, err := ReadFile(path); err != nil || !bytes.Equal(got, old) {
		held.Close()
		t.Fatalf("the refused replacement changed the file: %q, %v", got, err)
	}
	if left, _ := filepath.Glob(filepath.Join(filepath.Dir(path), "*")); len(left) != 1 {
		held.Close()
		t.Fatalf("the refused replacement left something beside the file: %v", left)
	}
	held.Close()
	if _, err := WriteReplace(path, next, 0o600); err != nil {
		t.Fatalf("once the handle let go: %v", err)
	}
}

// WHERE THE POSIX RENAME CANNOT BE MADE, THE EARLIER CALL IS, and its
// answer is the answer. A source that is read-only is one such place on
// any file system: the rename that flushes opens its source for writing,
// which a read-only file refuses, and the call every release before made
// moves it as it always did.
func TestAReadOnlySourceIsPublishedByTheEarlierCall(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	if _, err := WriteReplace(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	candidate := func() string {
		tmp := filepath.Join(dir, "candidate")
		if err := os.WriteFile(tmp, []byte("new"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(tmp, 0o400); err != nil {
			t.Fatal(err)
		}
		return tmp
	}
	tmp := candidate()
	if taken, renamed, err := posixRename(tmp, path, windows.FlushFileBuffers); taken || renamed || err == nil {
		t.Fatalf("rig: the flushing rename took a read-only source (taken=%v renamed=%v err=%v), so this test no longer reaches the earlier call", taken, renamed, err)
	}
	published, err := Replace(tmp, path)
	if err != nil || !published {
		t.Fatalf("a read-only source was not published: %v %v", published, err)
	}
	if got, err := ReadFile(path); err != nil || string(got) != "new" {
		t.Fatalf("the destination holds %q, %v", got, err)
	}
	if _, err := os.Stat(tmp); !os.IsNotExist(err) {
		t.Fatalf("the source still stands: %v", err)
	}
	// Let the temporary directory be removed.
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
}

// AND SO IS RENAME'S. No file system a release is tested on lacks the
// POSIX rename and a read-only source is no obstacle to a rename that
// asks no write access, so the rename is handed in as one that cannot be
// made: the earlier call moves the file, and where the earlier call fails
// too its answer is the one returned, in its own shape.
func TestWhereThePosixRenameIsNotMadeRenameIsTheEarlierCall(t *testing.T) {
	notHere := func(string, string, func(windows.Handle) error) (bool, bool, error) {
		return false, false, windows.ERROR_INVALID_PARAMETER
	}
	dir := t.TempDir()
	path, tmp := filepath.Join(dir, "state.json"), filepath.Join(dir, "candidate")
	mustWrite(t, path, "old")
	mustWrite(t, tmp, "new")
	if err := rename(tmp, path, notHere); err != nil {
		t.Fatalf("the earlier call was not made, or failed: %v", err)
	}
	if got, err := ReadFile(path); err != nil || string(got) != "new" {
		t.Fatalf("the destination holds %q, %v", got, err)
	}
	if _, err := os.Stat(tmp); !os.IsNotExist(err) {
		t.Fatalf("the source still stands: %v", err)
	}
	err := rename(filepath.Join(dir, "absent"), path, notHere)
	var link *os.LinkError
	if !errors.As(err, &link) || !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a source that is not there: %v; want the earlier call's own answer", err)
	}
}

// A FLUSH THAT FAILS AFTER THE NAME HAS MOVED SAYS PUBLISHED, with its
// error, once: the new file is at the name, the rename is not made again,
// and the earlier call is not tried on a source that is no longer there.
func TestAFlushThatFailsAfterTheRenameSaysPublished(t *testing.T) {
	dir := t.TempDir()
	path, tmp := filepath.Join(dir, "state.json"), filepath.Join(dir, "candidate")
	if _, err := WriteReplace(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tmp, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	// The error is one the retry reads as contention, the worst case: it
	// must still not be tried again.
	flushes := 0
	published, err := replace(tmp, path, func(windows.Handle) error {
		flushes++
		return windows.ERROR_ACCESS_DENIED
	})
	if !published || err != windows.ERROR_ACCESS_DENIED {
		t.Fatalf("published=%v err=%v; want published with the flush's own error", published, err)
	}
	if flushes != 1 {
		t.Fatalf("the flush ran %d times", flushes)
	}
	if got, rerr := ReadFile(path); rerr != nil || string(got) != "new" {
		t.Fatalf("the destination holds %q, %v", got, rerr)
	}
	if left, _ := filepath.Glob(filepath.Join(dir, "*")); len(left) != 1 {
		t.Fatalf("the directory holds more than the file: %v", left)
	}
}

// A PATH LONGER THAN THE OLD LIMIT is replaced under a reader like any
// other: the rename takes its new name whole, in the long form.
func TestALongPathIsReplacedUnderAReader(t *testing.T) {
	dir := t.TempDir()
	for len(dir) < 300 {
		dir = filepath.Join(dir, "a-directory-with-a-long-name-0123456789")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "state.json")
	if _, err := WriteReplace(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	reader, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	tmp := filepath.Join(dir, "candidate")
	if err := os.WriteFile(tmp, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	if taken, renamed, err := posixRename(tmp, path, windows.FlushFileBuffers); !taken || !renamed || err != nil {
		t.Fatalf("a path of %d characters: taken=%v renamed=%v err=%v", len(path), taken, renamed, err)
	}
	if err := Rename(mustWrite(t, tmp, "newer"), path); err != nil {
		t.Fatalf("Rename onto a path of %d characters under a reader: %v", len(path), err)
	}
	if got, err := ReadFile(path); err != nil || string(got) != "newer" {
		t.Fatalf("the destination holds %q, %v", got, err)
	}
}

func mustWrite(t *testing.T, path, content string) string {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
