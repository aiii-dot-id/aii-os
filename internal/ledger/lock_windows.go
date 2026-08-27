//go:build windows

package ledger

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

func lockLedgerFile(file *os.File) error {
	// LockFileEx ranges are mandatory. Use a cooperative sentinel beyond any
	// realizable ledger offset so this process's replay readers remain valid.
	const lockOffset = uint64(1) << 62
	overlapped := windows.Overlapped{
		Offset:     uint32(lockOffset & 0xffffffff),
		OffsetHigh: uint32(lockOffset >> 32),
	}
	err := windows.LockFileEx(windows.Handle(file.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0, 1, 0, &overlapped)
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return fmt.Errorf("%w: %w", ErrLedgerInUse, err)
	}
	return err
}

func openLedgerForRewrap(path string) (*os.File, error) {
	return openRewrapFile(path, windows.OPEN_EXISTING)
}

func createLedgerForRewrap(path string) (*os.File, error) {
	return openRewrapFile(path, windows.CREATE_NEW)
}

func openRewrapFile(path string, disposition uint32) (*os.File, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateFile(name,
		windows.GENERIC_READ|windows.GENERIC_WRITE|windows.DELETE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, disposition, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if errors.Is(err, windows.ERROR_SHARING_VIOLATION) {
		return nil, fmt.Errorf("%w: %w", ErrLedgerInUse, err)
	}
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	return os.NewFile(uintptr(handle), path), nil
}
