//go:build windows

package ledger

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

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
