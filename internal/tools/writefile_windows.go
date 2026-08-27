//go:build windows

package tools

import "os"

// writeFileNoFollow on Windows falls back to os.WriteFile: there is no
// O_NOFOLLOW, creating symlinks requires elevation, and the lexical+
// deepest-ancestor containment check remains the guard. An ACCEPTED
// residual, stated rather than hidden.
func writeFileNoFollow(path string, data []byte, perm os.FileMode) error {
	return os.WriteFile(path, data, perm)
}
