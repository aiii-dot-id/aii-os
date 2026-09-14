//go:build windows

package tools

import "os"

// .
// .
// .
// .
func writeFileNoFollow(path string, data []byte, perm os.FileMode) error {
	return os.WriteFile(path, data, perm)
}
