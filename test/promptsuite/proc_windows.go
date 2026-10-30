//go:build windows

package main

import (
	"os/exec"
	"syscall"

	"github.com/aiii-dot-id/aii-os/internal/install"
)

func ownGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
}

func askToStop(p *process) {
	_, _ = p.home.aii("stop", install.SlotName(0))
}

func killGroup(cmd *exec.Cmd) {
	_ = cmd.Process.Kill()
}
