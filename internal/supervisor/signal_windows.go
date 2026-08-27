//go:build windows

package supervisor

// Shutdown escalation, Windows half: there is no SIGTERM to deliver
// (os.Process.Signal supports only Kill on Windows), so the polite
// step is a documented no-op and the escalation goes EOF-grace →
// kill. The child's clean-shutdown path is the EOF (D1-1 rule 3),
// which works identically on Windows pipes.

import "os/exec"

func signalTerm(cmd *exec.Cmd) {
	// No-op: the caller's next escalation step is Kill.
	_ = cmd
}
