//go:build !windows

package tools

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
)

const shellDialect = "bash"

func shellInvocation() (string, []string) {
	return "bash", []string{"-c"}
}

func shellEnv(sandbox string) []string {
	return []string{
		"PATH=" + shellPath(runtime.GOOS),
		"HOME=" + sandbox,
		"LANG=C.UTF-8",
	}
}

func shellPath(goos string) string {
	const system = "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
	if goos == "darwin" {
		return "/opt/homebrew/bin:/opt/homebrew/sbin:" + system
	}
	return system
}

func prepareTree(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

type shellTree struct{}

func (t *shellTree) adopt(*exec.Cmd) {}

func (t *shellTree) kill(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}

func (t *shellTree) close() {}

func normalizeShellOutput(b []byte) []byte { return b }

func invocationExempt(token string) bool {
	clean := filepath.Clean(token)
	return filepath.IsAbs(clean) && (strings.HasPrefix(clean, "/usr/") || strings.HasPrefix(clean, "/bin/") ||
		clean == "/usr" || clean == "/bin" || clean == "/lib" || clean == "/etc/alternatives" ||
		clean == "/dev/null" || clean == "/dev/zero" || clean == "/dev/stdin" || clean == "/dev/stdout" || clean == "/dev/stderr")
}
