//go:build !windows

package main

import (
	"os/exec"
	"syscall"
)

func ownGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func askToStop(p *process) {
	_ = p.cmd.Process.Signal(syscall.SIGTERM)
}

func killGroup(cmd *exec.Cmd) {
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}
