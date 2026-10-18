//go:build windows

package filelock

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

const lockOffset = uint64(1) << 62

func Lock(f *os.File) error {
	return lockFile(f, windows.LOCKFILE_EXCLUSIVE_LOCK)
}

func RLock(f *os.File) error {
	return lockFile(f, 0)
}

func lockFile(f *os.File, flags uint32) error {
	overlapped := windows.Overlapped{
		Offset:     uint32(lockOffset & 0xffffffff),
		OffsetHigh: uint32(lockOffset >> 32),
	}
	err := windows.LockFileEx(windows.Handle(f.Fd()), flags|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &overlapped)
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return fmt.Errorf("%w: %w", ErrHeld, err)
	}
	return err
}
