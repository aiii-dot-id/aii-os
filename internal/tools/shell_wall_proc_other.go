//go:build !windows

package tools

import "os/exec"

func wallProcess(*exec.Cmd, shellWall) (func(), error) { return func() {}, nil }

func releaseGrant(string, []string) {}

const SandboxLabelNote = ""
