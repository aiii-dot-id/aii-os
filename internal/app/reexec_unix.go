//go:build (linux && !android) || (darwin && !ios)

package app

import (
	"os"
	"syscall"
)

// reexecSelf replaces this process image with the binary now at the
// executable path — after a rollback, the restored one. Does not
// return on success; the boot-health cycle restarts from zero in the
// binary that has already proven it can boot.
func reexecSelf() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	return syscall.Exec(exe, os.Args, os.Environ())
}
