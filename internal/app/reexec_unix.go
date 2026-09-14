//go:build (linux && !android) || (darwin && !ios)

package app

import (
	"os"
	"syscall"
)

// .
// .
// .
// .
func reexecSelf() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	return syscall.Exec(exe, os.Args, os.Environ())
}
