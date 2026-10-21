//go:build linux || android

package tools

import (
	"os"
	"strconv"
)

func openedAt(by *os.File) (string, error) {
	raw, err := by.SyscallConn()
	if err != nil {
		return "", err
	}
	var at string
	var readErr error
	if err := raw.Control(func(fd uintptr) {
		at, readErr = os.Readlink("/proc/self/fd/" + strconv.FormatUint(uint64(fd), 10))
	}); err != nil {
		return "", err
	}
	return at, readErr
}
