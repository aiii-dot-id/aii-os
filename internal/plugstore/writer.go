//go:build (linux && !android) || (darwin && !ios) || windows

package plugstore

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/aiii-dot-id/aii-os/internal/filelock"
	"github.com/aiii-dot-id/aii-os/internal/fileperm"
)

var ErrHeld = errors.New("another writer holds this plugin store")

type NoHardLinksError struct {
	Dir   string
	Cause error
}

func (e *NoHardLinksError) Error() string {
	return fmt.Sprintf("the plugin store %s cannot be used: a hard link cannot be made inside it (%v) — the store needs a volume that keeps hard links (NTFS, APFS, ext4, xfs, btrfs); name another place with: aii machine store <dir>", e.Dir, e.Cause)
}

func (e *NoHardLinksError) Unwrap() error { return e.Cause }

type Writer struct {
	Store
	lock *os.File
}

func OpenWriter(dir string) (*Writer, error) {
	if !filepath.IsAbs(dir) {
		return nil, fmt.Errorf("the plugin store %q is not an absolute path", dir)
	}
	dir = filepath.Clean(dir)
	if err := fileperm.EnsureOwnerOnlyDir(dir); err != nil {
		return nil, fmt.Errorf("the plugin store: %w", err)
	}
	lock, err := os.OpenFile(filepath.Join(dir, LockFile), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open the plugin store's lock: %w", err)
	}
	if err := filelock.Lock(lock); err != nil {
		lock.Close()
		if errors.Is(err, filelock.ErrHeld) {
			return nil, fmt.Errorf("%w (%s)", ErrHeld, dir)
		}
		return nil, fmt.Errorf("take the plugin store's lock: %w", err)
	}
	w := &Writer{Store: Store{Dir: dir}, lock: lock}
	for _, d := range []string{DirArchives, DirRoots, DirBlobs, DirViews, DirTrash} {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o700); err != nil {
			w.Close()
			return nil, fmt.Errorf("the plugin store: %w", err)
		}
	}
	if err := w.probeHardLink(); err != nil {
		w.Close()
		return nil, err
	}
	return w, nil
}

func (w *Writer) probeHardLink() error {
	name := ".probe-" + randomHex(8)
	src := filepath.Join(w.Dir, DirBlobs, name)
	dst := filepath.Join(w.Dir, DirViews, name)
	if err := os.WriteFile(src, nil, 0o600); err != nil {
		return fmt.Errorf("the plugin store: %w", err)
	}
	defer os.Remove(src)
	if err := os.Link(src, dst); err != nil {
		return &NoHardLinksError{Dir: w.Dir, Cause: err}
	}
	defer os.Remove(dst)
	a, aerr := os.Stat(src)
	b, berr := os.Stat(dst)
	if aerr != nil || berr != nil || !os.SameFile(a, b) {
		return &NoHardLinksError{Dir: w.Dir, Cause: errors.Join(aerr, berr, errors.New("the link is not the file"))}
	}
	return nil
}

func (w *Writer) Close() error {
	if w == nil || w.lock == nil {
		return nil
	}
	err := w.lock.Close()
	w.lock = nil
	return err
}

func (w *Writer) Trash() string {
	return filepath.Join(w.Dir, DirTrash, randomHex(16))
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
