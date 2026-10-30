//go:build !windows && !(linux && !android)

package tools

import (
	"syscall"
	"testing"
)

func shellProcess(_ *testing.T, _ *ShellTool, pid int) (int, bool) {
	return pid, syscall.Kill(pid, 0) != syscall.ESRCH
}
