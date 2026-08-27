//go:build !windows

package supervisor

// Shutdown escalation, unix half: after the EOF grace, one polite
// SIGTERM before the kill (D1-1 rule 3 makes EOF the clean disconnect;
// TERM is for a child that ignored it).

import (
	"os/exec"
	"syscall"
)

func signalTerm(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = cmd.Process.Signal(syscall.SIGTERM)
	}
}
