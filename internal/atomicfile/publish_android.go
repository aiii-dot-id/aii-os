//go:build android

package atomicfile

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// .
// .
// .
// .
// .
func PublishNew(oldPath, newPath string) (published bool, err error) {
	dir, err := os.Open(filepath.Dir(newPath))
	if err != nil {
		return false, err
	}
	defer dir.Close()
	if err := unix.Renameat2(unix.AT_FDCWD, oldPath, unix.AT_FDCWD, newPath, unix.RENAME_NOREPLACE); err != nil {
		return false, err
	}
	if err := dir.Sync(); err != nil {
		return true, err
	}
	return true, nil
}
