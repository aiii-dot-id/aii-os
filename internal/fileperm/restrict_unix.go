//go:build !windows

package fileperm

import (
	"os"
	"path/filepath"
)

func CreateTempForReplacement(path string) (*os.File, error) {
	return os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
}

func RestrictToOwner(f *os.File) error {
	return f.Chmod(0o600)
}

func IsRestrictedToOwner(path string) (bool, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return false, err
	}
	return fi.Mode().Perm()&0o077 == 0, nil
}

func IsClosedToOthers(path string) (bool, error) {
	return IsRestrictedToOwner(path)
}
