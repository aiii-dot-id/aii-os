//go:build windows

package plugstore

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestAnOpenStoreFileIsNeverTakenFromItsHolder(t *testing.T) {
	w := openWriter(t)
	key := "0123456789abcdef"
	view, record := publishView(t, w, key)
	model := filepath.Join(view, "model.bin")
	open, err := os.Open(model)
	if err != nil {
		t.Fatal(err)
	}
	defer open.Close()
	pin, err := w.PinView(key)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Remove(Entry{Kind: DirViews, ID: key}); !errors.Is(err, ErrPinned) {
		t.Fatalf("removing a pinned view whose file is open: want ErrPinned, got %v", err)
	}
	pin.Close()
	err = w.Remove(Entry{Kind: DirViews, ID: key})
	switch {
	case errors.Is(err, ErrBusy):
		for _, path := range []string{view, record, model} {
			if _, err := os.Stat(path); err != nil {
				t.Fatalf("a view skipped as busy lost %s: %v", path, err)
			}
		}
	case err != nil:
		t.Fatalf("removing an unpinned view whose file is open: %v", err)
	}
	t.Logf("an unpinned view whose file is open: %v", err)
	blob, _ := w.Blob(sha)
	berr := w.Remove(Entry{Kind: DirBlobs, ID: sha})
	if berr != nil && !errors.Is(berr, ErrBusy) {
		t.Fatalf("removing a blob open through a view: %v", berr)
	}
	t.Logf("a blob's b/ name while its file is open through a view: %v", berr)
	if _, err := open.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(open)
	if err != nil || string(got) != "model bytes" {
		t.Fatalf("the holder's open file after its other names went: %q, %v", got, err)
	}
	if errors.Is(berr, ErrBusy) {
		if _, err := os.Stat(blob); err != nil {
			t.Fatalf("a blob skipped as busy is gone: %v", err)
		}
	}
	open.Close()
	if err := w.Remove(Entry{Kind: DirViews, ID: key}); err != nil {
		t.Fatalf("once closed, the view is removed: %v", err)
	}
	if err := w.Remove(Entry{Kind: DirBlobs, ID: sha}); err != nil {
		t.Fatalf("once closed, the blob is removed: %v", err)
	}
}
