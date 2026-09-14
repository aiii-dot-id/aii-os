//go:build windows

package atomicfile

import (
	"fmt"
	"os"

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
func ReplaceExecutable(srcPath, dstPath string) (published bool, err error) {
	oldPath := dstPath + ".old"
	_ = os.Remove(oldPath)
	if err := moveThrough(dstPath, oldPath); err != nil {
		// .
		return false, fmt.Errorf("rename running image aside: %w", err)
	}
	if err := moveThrough(srcPath, dstPath); err != nil {
		// .
		// .
		if rerr := moveThrough(oldPath, dstPath); rerr != nil {
			// .
			// .
			// .
			// .
			return true, fmt.Errorf("publish failed (%v) AND restore failed (%v) — the binary is at %s", err, rerr, oldPath)
		}
		return false, fmt.Errorf("publish new image: %w", err)
	}
	return true, nil
}

// .
// .
// .
// .
// .
func moveThrough(from, to string) error {
	fromName, err := windows.UTF16PtrFromString(from)
	if err != nil {
		return err
	}
	toName, err := windows.UTF16PtrFromString(to)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(fromName, toName, windows.MOVEFILE_WRITE_THROUGH)
}
