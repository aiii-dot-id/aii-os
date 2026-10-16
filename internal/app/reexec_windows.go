//go:build windows

package app

import (
	"os"
	"os/exec"
	"strconv"

	"github.com/aiii-dot-id/aii-os/internal/consolewin"
)

func reexecSelf() error {
	cmd, err := handOverCommand()
	if err != nil {
		return err
	}
	return cmd.Start()
}

func handOverCommand() (*exec.Cmd, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(exe, os.Args[1:]...)
	cmd.Stdout, cmd.Stderr, cmd.Stdin = os.Stdout, os.Stderr, os.Stdin

	cmd.Env = append(os.Environ(), handoverFromEnv+"="+strconv.Itoa(os.Getpid()))

	if !consolewin.Visible() {
		consolewin.Hide(cmd)
	}
	return cmd, nil
}
