//go:build !windows

package atomicfile

import "os"

// .
// .
// .
// .
func OpenAppendRemovable(path string, perm os.FileMode) (*os.File, error) {
	return os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_APPEND, perm)
}
