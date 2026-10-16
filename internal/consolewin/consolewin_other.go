//go:build !windows

package consolewin

import "os/exec"

func Hide(*exec.Cmd) {}

func Visible() bool { return false }
