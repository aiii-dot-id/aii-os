//go:build windows

package app

import (
	"os"
	"os/exec"

	"github.com/aiii-dot-id/aii-os/internal/consolewin"
)

func reexecSelf() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(exe, os.Args[1:]...)
	cmd.Stdout, cmd.Stderr, cmd.Stdin = os.Stdout, os.Stderr, os.Stdin

	if !consolewin.Visible() {
		consolewin.Hide(cmd)
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	os.Exit(0)
	return nil
}
