//go:build windows

package atomicfile

import (
	"errors"

	"golang.org/x/sys/windows"
)

func Replace(oldPath, newPath string) (bool, error) {
	before, err := openEarlier(oldPath, newPath)
	if err != nil {
		return false, err
	}
	published, err := replace(oldPath, newPath, windows.FlushFileBuffers)
	if !published {
		before.release()
		return false, err
	}
	return true, errors.Join(err, before.empty(newPath))
}

func replaceStaged(staged, newPath string) (bool, error) {
	return Replace(staged, newPath)
}

func replace(oldPath, newPath string, flush func(windows.Handle) error) (bool, error) {
	oldName, err := windows.UTF16PtrFromString(oldPath)
	if err != nil {
		return false, err
	}
	newName, err := windows.UTF16PtrFromString(newPath)
	if err != nil {
		return false, err
	}
	var published bool
	var flushErr error
	err = retryContended(func() error {
		taken, renamed, err := posixRename(oldPath, newPath, flush)
		if renamed {

			published, flushErr = true, err
			return nil
		}
		if taken {
			return err
		}
		return windows.MoveFileEx(oldName, newName, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
	}, renameContended)
	if err != nil {
		return false, err
	}
	if published {
		return true, flushErr
	}
	return true, nil
}

func PublishNew(oldPath, newPath string) (bool, error) {
	oldName, err := windows.UTF16PtrFromString(oldPath)
	if err != nil {
		return false, err
	}
	newName, err := windows.UTF16PtrFromString(newPath)
	if err != nil {
		return false, err
	}
	if err := moveWithRetry(oldName, newName, windows.MOVEFILE_WRITE_THROUGH); err != nil {
		return false, err
	}
	return true, nil
}

func moveWithRetry(from, to *uint16, flags uint32) error {
	return retryContended(func() error { return windows.MoveFileEx(from, to, flags) }, renameContended)
}
