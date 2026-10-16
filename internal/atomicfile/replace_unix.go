//go:build !windows

package atomicfile

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
)

func Replace(oldPath, newPath string) (published bool, err error) {
	dir, err := os.Open(filepath.Dir(newPath))
	if err != nil {
		return false, err
	}
	defer dir.Close()
	return replace(oldPath, newPath, dir.Sync)
}

func replace(oldPath, newPath string, syncDir func() error) (bool, error) {
	before, err := openEarlier(oldPath, newPath)
	if err != nil {
		return false, err
	}
	if replaceWindow != nil {
		replaceWindow(newPath)
	}
	if err := os.Rename(oldPath, newPath); err != nil {
		before.release()
		return false, err
	}
	if err := syncDir(); err != nil {
		if before != nil {
			before.release()
			err = errors.Join(err, fmt.Errorf("the file %s replaced was not emptied, since the move of its name is not known to last: another name of it may hold its earlier contents", newPath))
		}
		return true, err
	}
	return true, before.empty(newPath)
}

func replaceStaged(staged, newPath string) (published bool, err error) {
	dir, err := os.Open(filepath.Dir(newPath))
	if err != nil {
		return false, err
	}
	defer dir.Close()
	return replaceStagedWith(staged, newPath, dir.Sync)
}

func replaceStagedWith(staged, newPath string, syncDir func() error) (bool, error) {
	if published, done, err := byExchange(staged, newPath, syncDir); done {
		return published, err
	}
	return replace(staged, newPath, syncDir)
}

var exchangeNames = exchange

var replaceWindow func(newPath string)

func byExchange(oldPath, newPath string, syncDir func() error) (published, done bool, err error) {
	at, err := os.Lstat(newPath)
	if err != nil || !at.Mode().IsRegular() {
		return false, false, nil
	}
	if src, err := os.Lstat(oldPath); err != nil || os.SameFile(src, at) {
		return false, false, nil
	}
	if replaceWindow != nil {
		replaceWindow(newPath)
	}
	if err := exchangeNames(oldPath, newPath); err != nil {
		if noExchange(err) {
			return false, false, nil
		}
		return false, true, &os.LinkError{Op: "swap", Old: oldPath, New: newPath, Err: err}
	}
	if err := syncDir(); err != nil {
		return true, true, errors.Join(err, fmt.Errorf("the file %s replaced, now at %s, was not emptied, since the swap is not known to last: another name of it may hold its earlier contents", newPath, oldPath))
	}
	if err := Remove(oldPath); err != nil {
		return true, true, fmt.Errorf("the file %s replaced: %w", newPath, err)
	}
	if err := syncDir(); err != nil {
		return true, true, fmt.Errorf("the file %s replaced is removed from %s, but its removal is not known to last: %w", newPath, oldPath, err)
	}
	return true, true, nil
}

func noExchange(err error) bool {
	return errors.Is(err, errors.ErrUnsupported) || errors.Is(err, fs.ErrNotExist) ||
		errors.Is(err, syscall.EINVAL) || errors.Is(err, syscall.ENOSYS) || errors.Is(err, syscall.EXDEV) ||
		errors.Is(err, syscall.ENOTSUP) || errors.Is(err, syscall.EOPNOTSUPP) ||
		errors.Is(err, syscall.EACCES) || errors.Is(err, syscall.EPERM)
}

func publish(oldPath, newPath string) (published bool, err error) {
	dir, err := os.Open(filepath.Dir(newPath))
	if err != nil {
		return false, err
	}
	defer dir.Close()
	if err := os.Rename(oldPath, newPath); err != nil {
		return false, err
	}
	if err := dir.Sync(); err != nil {
		return true, err
	}
	return true, nil
}
