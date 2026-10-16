//go:build (linux && !android) || (darwin && !ios) || windows

package plugstore

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/aiii-dot-id/aii-os/internal/filelock"
	"github.com/aiii-dot-id/aii-os/internal/fileperm"
)

var ErrStoreGone = errors.New("the plugin store is not there")

var ErrStoreUnreachable = errors.New("the plugin store's volume is not there")

func OpenForRemoval(dir string) (*Writer, error) {
	if !filepath.IsAbs(dir) {
		return nil, fmt.Errorf("the plugin store %q is not an absolute path", dir)
	}
	dir = filepath.Clean(dir)
	fi, err := os.Lstat(dir)
	if errors.Is(err, fs.ErrNotExist) {
		if pi, perr := os.Stat(filepath.Dir(dir)); perr == nil && pi.IsDir() {
			return nil, fmt.Errorf("%w (%s)", ErrStoreGone, dir)
		}
		return nil, fmt.Errorf("%w (%s)", ErrStoreUnreachable, dir)
	}
	if err != nil {
		return nil, fmt.Errorf("the plugin store %s: %w", dir, err)
	}
	switch {
	case fi.Mode()&os.ModeSymlink != 0:
		return nil, fmt.Errorf("the plugin store %s is a link: a store is removed only where it lies", dir)
	case !fi.IsDir():
		return nil, fmt.Errorf("the plugin store %s is not a directory", dir)
	}
	if ok, err := fileperm.IsOwnerOnlyDir(dir); err != nil || !ok {
		return nil, fmt.Errorf("the plugin store %s is not this account's own (%v): %w", dir, err, fileperm.ErrNotOwnerOnly)
	}
	lock, err := os.OpenFile(filepath.Join(dir, LockFile), os.O_RDWR, 0)
	if errors.Is(err, fs.ErrNotExist) {
		return &Writer{Store: Store{Dir: dir}}, nil
	}
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
	return &Writer{Store: Store{Dir: dir}, lock: lock}, nil
}
