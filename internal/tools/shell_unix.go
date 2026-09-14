//go:build !windows

// .
// .
// .
package tools

import (
	"os/exec"
	"syscall"
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
// .
const shellDialect = "bash"

// .
// .
// .
// .
func shellInvocation() (string, []string) {
	return "bash", []string{"-c"}
}

// .
// .
// .
func shellEnv(sandbox string) []string {
	return []string{
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"HOME=" + sandbox,
		"LANG=C.UTF-8",
	}
}

// .
// .
// .
func prepareTree(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// .
// .
type shellTree struct{}

func (t *shellTree) adopt(*exec.Cmd) {}

// .
// .
// .
func (t *shellTree) kill(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}

func (t *shellTree) close() {}

// .
// .
// .
func normalizeShellOutput(b []byte) []byte { return b }
