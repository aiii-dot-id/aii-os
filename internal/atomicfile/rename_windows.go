//go:build windows

package atomicfile

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

func Rename(from, to string) error { return rename(from, to, posixRename) }

func rename(from, to string, posix func(from, to string, flush func(windows.Handle) error) (taken, renamed bool, err error)) error {
	return retryContended(func() error {
		taken, _, err := posix(from, to, nil)
		if taken {
			if err != nil {
				return &os.LinkError{Op: "rename", Old: from, New: to, Err: err}
			}
			return nil
		}
		return os.Rename(from, to)
	}, renameContended)
}

func renameContended(err error) bool {
	return errors.Is(err, windows.ERROR_ACCESS_DENIED) ||
		errors.Is(err, windows.ERROR_SHARING_VIOLATION)
}
