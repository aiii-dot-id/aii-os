//go:build !windows

package atomicfile

import (
	"os"
	"path/filepath"
)

// Replace atomically publishes oldPath at newPath and syncs the directory.
// published distinguishes a pre-publication failure from a durability failure
// after rename, when callers must follow the file now visible at newPath.
func Replace(oldPath, newPath string) (published bool, err error) {
	dir, err := os.Open(filepath.Dir(newPath))
	if err != nil {
		return false, err
	}
	defer dir.Close()
	return replace(oldPath, newPath, dir.Sync)
}

// PublishNew atomically gives a prepared file a new name without replacing an
// existing file. The hard link and temporary-name removal become durable in
// one directory sync.
func PublishNew(oldPath, newPath string) (published bool, err error) {
	dir, err := os.Open(filepath.Dir(newPath))
	if err != nil {
		return false, err
	}
	defer dir.Close()
	if err := os.Link(oldPath, newPath); err != nil {
		return false, err
	}
	if err := os.Remove(oldPath); err != nil {
		return true, err
	}
	if err := dir.Sync(); err != nil {
		return true, err
	}
	return true, nil
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
