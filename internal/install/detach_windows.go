//go:build windows

package install

import (
	"os/exec"
	"syscall"
)

// .
// .
// .
const (
	detachedProcess        = 0x00000008
	createNoWindow         = 0x08000000
	createBreakawayFromJob = 0x01000000
)

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
func StartDetached(cmd *exec.Cmd) error {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: detachedProcess | createNoWindow | createBreakawayFromJob,
	}
	if err := cmd.Start(); err == nil {
		return nil
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: detachedProcess | createNoWindow,
	}
	return cmd.Start()
}
