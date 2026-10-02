package atomicfile

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/aiii-dot-id/aii-os/internal/fileperm"
)

func WriteReplace(path string, data []byte, perm os.FileMode) (published bool, err error) {
	tmp, err := stage(path, data, perm)
	if err != nil {
		return false, err
	}
	published, err = Replace(tmp, path)
	return published, unstage(tmp, err)
}

func WriteNew(path string, data []byte, perm os.FileMode) (published bool, err error) {
	tmp, err := stage(path, data, perm)
	if err != nil {
		return false, err
	}
	published, err = PublishNew(tmp, path)
	return published, unstage(tmp, err)
}

func stage(path string, data []byte, perm os.FileMode) (tmp string, err error) {
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return "", fmt.Errorf("create temporary file: %w", err)
	}
	tmp = f.Name()
	closed := false
	defer func() {
		if err == nil {
			return
		}
		if !closed {
			f.Close()
		}
		err = unstage(tmp, err)
	}()
	if perm&0o077 == 0 {
		if err := fileperm.RestrictToOwner(f); err != nil {
			return "", fmt.Errorf("restrict temporary file to its owner: %w", err)
		}
	}
	if err := f.Chmod(perm); err != nil {
		return "", fmt.Errorf("set temporary file mode: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		return "", fmt.Errorf("write temporary file: %w", err)
	}
	if err := f.Sync(); err != nil {
		return "", fmt.Errorf("sync temporary file: %w", err)
	}
	closed = true
	if err := f.Close(); err != nil {
		return "", fmt.Errorf("close temporary file: %w", err)
	}
	return tmp, nil
}

func unstage(tmp string, err error) error {
	if err == nil {
		return nil
	}
	if rerr := os.Remove(tmp); rerr != nil && !errors.Is(rerr, os.ErrNotExist) {
		return errors.Join(err, fmt.Errorf("remove temporary file: %w", rerr))
	}
	return err
}
