//go:build windows

package filelock

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

func Lock(f *os.File) error {

	const lockOffset = uint64(1) << 62
	overlapped := windows.Overlapped{
		Offset:     uint32(lockOffset & 0xffffffff),
		OffsetHigh: uint32(lockOffset >> 32),
	}
	err := windows.LockFileEx(windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0, 1, 0, &overlapped)
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return fmt.Errorf("%w: %w", ErrHeld, err)
	}
	return err
}
