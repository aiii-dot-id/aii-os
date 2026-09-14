//go:build !windows

package supervisor

// .
// .
// .

import (
	"os/exec"
	"syscall"
)

func signalTerm(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = cmd.Process.Signal(syscall.SIGTERM)
	}
}
