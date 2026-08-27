//go:build windows

package app

import (
	"os"
	"os/exec"
)

// reexecSelf hands over to the restored binary — Windows has no
// execve, so: spawn it as a child on the same argv and stdio, then
// exit. The displaced image this process is running from stays as
// .old until the child's checkRollbackAt removes it.
func reexecSelf() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(exe, os.Args[1:]...)
	cmd.Stdout, cmd.Stderr, cmd.Stdin = os.Stdout, os.Stderr, os.Stdin
	if err := cmd.Start(); err != nil {
		return err
	}
	os.Exit(0)
	return nil // unreachable
}
