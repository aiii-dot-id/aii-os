//go:build windows

package install

import (
	"errors"
	"os"
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

const (
	detachedProcess        = 0x00000008
	createNoWindow         = 0x08000000
	createBreakawayFromJob = 0x01000000
)

func StartDetached(command func() *exec.Cmd) (*os.Process, error) {
	return startDetached(command, (*exec.Cmd).Start)
}

func startDetached(command func() *exec.Cmd, start func(*exec.Cmd) error) (*os.Process, error) {
	attempt := func(flags uint32) (*os.Process, error) {
		cmd := command()
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: flags}
		if err := start(cmd); err != nil {
			return nil, err
		}
		return cmd.Process, nil
	}
	p, err := attempt(detachedProcess | createNoWindow | createBreakawayFromJob)
	if !errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		return p, err
	}
	return attempt(detachedProcess | createNoWindow)
}
