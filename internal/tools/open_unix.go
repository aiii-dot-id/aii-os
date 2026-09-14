//go:build !windows

package tools

import (
	"os"
	"syscall"
)

// .
// .
// .
// .
func openNonBlocking(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
}
