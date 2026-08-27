//go:build !windows

// shell_unix.go — the unix host facts for the shell organ (R79):
// dialect, invocation, scrubbed environment, and tree containment.
// tool_shell.go is the tool; this file is what THIS host speaks.
package tools

import (
	"os/exec"
	"syscall"
)

// shellDialect names the language the identity writes in the shell
// tool — declared in the tool's own description, never discovered by
// failure.
// One dialect, two vintages: macOS ships bash 3.2.57 (GPLv2-frozen,
// verified 2026-08-27 on Tahoe) while linux carries 5.x — bash-5
// idioms (associative arrays, coproc) fail on a Mac. The version is
// one command away for the identity (bash --version); declaring it
// here would cost a boot-time subprocess for information the shell
// answers itself.
//
// The !windows tag also compiles this file on android/ios, where the
// tool is never registered (hostcap.Shell refuses) — dead code behind
// the gate. If Android ever grows a shell, its fact is /system/bin/sh
// (no bash): fork a shell_android.go leg and narrow this tag rather
// than widening this file.
const shellDialect = "bash"

// shellInvocation returns the host shell binary and the argv prefix
// the command is appended to. One exec call site consumes this
// (tool_shell.go); the strings live here so the tool never branches
// on GOOS.
func shellInvocation() (string, []string) {
	return "bash", []string{"-c"}
}

// shellEnv is the scrubbed child environment: cwd/HOME are the
// sandbox, PATH is the system default, and nothing of the host's own
// environment — keys, service vars, operator secrets — crosses over.
func shellEnv(sandbox string) []string {
	return []string{
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"HOME=" + sandbox,
		"LANG=C.UTF-8",
	}
}

// prepareTree gives the shell its own process group before it starts,
// so the ceiling can address everything the command spawned — not
// just the shell.
func prepareTree(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// shellTree is the unix tree handle. The group IS the tree, made at
// start — adopt and close have nothing to do.
type shellTree struct{}

func (t *shellTree) adopt(*exec.Cmd) {}

// kill delivers SIGKILL to the whole group. Called by exec.Cmd as the
// Cancel hook once the context ends; the negative pid is the group
// address.
func (t *shellTree) kill(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}

func (t *shellTree) close() {}
