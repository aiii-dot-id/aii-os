//go:build windows

package install

import (
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

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
// .
// .
// .
// .
func Stop(slot string) error {
	root, err := Root()
	if err != nil {
		return err
	}
	dir := filepath.Join(root, slot)
	if _, err := os.Stat(dir); err != nil {
		return fmt.Errorf("%s does not exist", dir)
	}
	var lastErr error
	for _, name := range StopChannelNames(dir) {
		n, err := windows.UTF16PtrFromString(name)
		if err != nil {
			lastErr = err
			continue
		}
		h, err := windows.OpenEvent(windows.EVENT_MODIFY_STATE, false, n)
		if err != nil {
			lastErr = err
			continue
		}
		err = windows.SetEvent(h)
		windows.CloseHandle(h)
		if err != nil {
			return fmt.Errorf("signal stop for %s: %w", slot, err)
		}
		return nil
	}
	// .
	// .
	// .
	if lastErr != nil {
		return ErrNotRunning
	}
	return ErrNotRunning
}

// .
var ErrNotRunning = fmt.Errorf("not running")
