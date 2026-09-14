//go:build windows

package supervisor

// .
// .
// .
// .
// .

import "os/exec"

func signalTerm(cmd *exec.Cmd) {
	// .
	_ = cmd
}
