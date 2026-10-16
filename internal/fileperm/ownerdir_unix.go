//go:build !windows

package fileperm

import (
	"os"
	"path/filepath"
)

func MkdirOwnerOnly(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.Mkdir(path, 0o700)
}

func IsOwnerOnlyDir(path string) (bool, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return false, err
	}
	return fi.IsDir() && fi.Mode().Perm()&0o077 == 0, nil
}
