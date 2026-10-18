//go:build (linux && !android) || (darwin && !ios) || windows

package plugstore

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/filelock"
)

// A PIN KEEPS THE COLLECTOR OUT. While a reader holds a view's record
// shared, the collector's removal of it is refused (ErrPinned) and nothing
// of the entry moves; once the pin is let go, it is removed whole.
func TestAHeldPinMakesTheCollectorsExclusiveTryFail(t *testing.T) {
	w := openWriter(t)
	key := "0123456789abcdef"
	view, record := publishView(t, w, key)
	p, err := w.PinView(key)
	if err != nil {
		t.Fatalf("pin: %v", err)
	}
	if err := w.Remove(Entry{Kind: DirViews, ID: key}); !errors.Is(err, ErrPinned) {
		t.Fatalf("removing a pinned view: want ErrPinned, got %v", err)
	}
	for _, path := range []string{view, record, filepath.Join(view, "model.bin")} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("a pinned entry lost %s: %v", path, err)
		}
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	if err := w.Remove(Entry{Kind: DirViews, ID: key}); err != nil {
		t.Fatalf("removing an unpinned view: %v", err)
	}
	for _, path := range []string{view, record} {
		if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("a removed entry left %s: %v", path, err)
		}
	}
	if left, _ := os.ReadDir(filepath.Join(w.Dir, DirTrash)); len(left) != 0 {
		t.Errorf("the trash keeps %d entries after a removal", len(left))
	}
	// And while the collector holds the record, a reader's pin is refused,
	// not granted on a record that is going.
	_, _ = publishView(t, w, key)
	path, _ := w.ViewRecord(key)
	lock, err := openForCollect(path)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := filelock.Lock(lock); err != nil {
		t.Fatal(err)
	}
	if _, err := w.PinView(key); !errors.Is(err, ErrCollecting) {
		t.Fatalf("a pin while the collector holds the record: want ErrCollecting, got %v", err)
	}
}

// A RECORD REPLACED AFTER ITS OPEN IS REFUSED: the reader locked a file the
// path no longer names (the collector moved it aside, a new one was
// published), and holding it would protect nothing.
func TestARecordReplacedAfterOpenIsRefused(t *testing.T) {
	w := openWriter(t)
	key := "fedcba9876543210"
	_, record := publishView(t, w, key)
	f, err := os.Open(record)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		// Windows moves no file a handle holds without delete sharing; the
		// collector's own move is refused there instead (ErrBusy), which
		// TestARemovalOfAnOpenFileIsSkippedOnWindows holds.
		f.Close()
		t.Skip("Windows refuses the replacement itself")
	}
	if err := os.Rename(record, filepath.Join(w.Dir, DirTrash, "old")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(record, []byte(`{"format":1,"models_sha256":"x","names":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if p, err := hold(f, record); !errors.Is(err, ErrReplaced) {
		p.Close()
		t.Fatalf("a record replaced after its open: want ErrReplaced, got %v", err)
	}
	if err := os.Remove(record); err != nil {
		t.Fatal(err)
	}
	f2, err := os.Open(filepath.Join(w.Dir, DirTrash, "old"))
	if err != nil {
		t.Fatal(err)
	}
	if p, err := hold(f2, record); !errors.Is(err, ErrReplaced) {
		p.Close()
		t.Fatalf("a record removed after its open: want ErrReplaced, got %v", err)
	}
}

// ONE WRITER: a second writer on the same store gets ErrHeld, and the
// first's close lets the next one in.
func TestASecondWriterOnTheSameStoreGetsErrHeld(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "plugins")
	w, err := OpenWriter(dir)
	if err != nil {
		t.Fatal(err)
	}
	if second, err := OpenWriter(dir); !errors.Is(err, ErrHeld) {
		if second != nil {
			second.Close()
		}
		t.Fatalf("a second writer: want ErrHeld, got %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	again, err := OpenWriter(dir)
	if err != nil {
		t.Fatalf("a writer after the first closed: %v", err)
	}
	again.Close()
	for _, d := range []string{DirArchives, DirRoots, DirBlobs, DirViews, DirTrash} {
		if fi, err := os.Stat(filepath.Join(dir, d)); err != nil || !fi.IsDir() {
			t.Errorf("the store has no %s/: %v", d, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "p")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("p/ is L2's and is not made: %v", err)
	}
}

func TestAStoreOpenToOtherAccountsIsRefused(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the Windows spelling of an open directory is a DACL: fileperm's own tests hold it")
	}
	dir := filepath.Join(t.TempDir(), "plugins")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if w, err := OpenWriter(dir); err == nil {
		w.Close()
		t.Fatal("a store open to other accounts was taken")
	}
}

func openWriter(t *testing.T) *Writer {
	t.Helper()
	w, err := OpenWriter(filepath.Join(t.TempDir(), "plugins"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { w.Close() })
	return w
}

// publishView makes a view with one link, and its record.
func publishView(t *testing.T, w *Writer, key string) (view, record string) {
	t.Helper()
	blob, _ := w.Blob(sha)
	if err := os.WriteFile(blob, []byte("model bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	view, _ = w.View(key)
	record, _ = w.ViewRecord(key)
	if err := os.MkdirAll(view, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(blob, filepath.Join(view, "model.bin")); err != nil && !errors.Is(err, fs.ErrExist) {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(ViewRecord{ModelsSHA256: "sha256:" + sha, Names: []string{"m"}})
	if err := os.WriteFile(record, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return view, record
}
