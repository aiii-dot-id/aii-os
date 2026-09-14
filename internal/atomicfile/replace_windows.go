//go:build windows

package atomicfile

import (
	"time"

	"golang.org/x/sys/windows"
)

// .
func Replace(oldPath, newPath string) (bool, error) {
	oldName, err := windows.UTF16PtrFromString(oldPath)
	if err != nil {
		return false, err
	}
	newName, err := windows.UTF16PtrFromString(newPath)
	if err != nil {
		return false, err
	}
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	if err := moveWithRetry(oldName, newName,
		windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH); err != nil {
		return false, err
	}
	return true, nil
}

// .
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

// .
// .
func moveWithRetry(from, to *uint16, flags uint32) error {
	deadline := time.Now().Add(renameRetryBudget)
	delay := 500 * time.Microsecond
	for {
		err := windows.MoveFileEx(from, to, flags)
		if err == nil || !renameContended(err) || time.Now().After(deadline) {
			return err
		}
		time.Sleep(delay)
		if delay < 20*time.Millisecond {
			delay *= 2
		}
	}
}
