//go:build !windows

package atomicfile

import (
	"os"
	"path/filepath"
)

// .
// .
// .
func Replace(oldPath, newPath string) (published bool, err error) {
	dir, err := os.Open(filepath.Dir(newPath))
	if err != nil {
		return false, err
	}
	defer dir.Close()
	return replace(oldPath, newPath, dir.Sync)
}

func replace(oldPath, newPath string, syncDir func() error) (bool, error) {
	if err := os.Rename(oldPath, newPath); err != nil {
		return false, err
	}
	if err := syncDir(); err != nil {
		return true, err
	}
	return true, nil
}
