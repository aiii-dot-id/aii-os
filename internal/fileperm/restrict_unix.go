//go:build !windows

package fileperm

import "os"

// .
// .
// .
func RestrictToOwner(f *os.File) error {
	return f.Chmod(0o600)
}

// .
// .
// .
// .
func IsRestrictedToOwner(path string) (bool, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return false, err
	}
	return fi.Mode().Perm()&0o077 == 0, nil
}
