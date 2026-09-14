//go:build windows

package tools

import "os"

// .
// .
func openNonBlocking(path string) (*os.File, error) {
	return os.Open(path)
}
