//go:build windows

package main

import (
	"fmt"
	"os/exec"
)

// reachProbe: the Windows wall is tried from inside by the supervisor's
// own AppContainer tests, with a child built for them.
func reachProbe(args []string) { fmt.Println("reach=unsupported") }

// carrierProbe: the Windows wall grants a child its runtime root to read
// and execute by the AppContainer's own rules, tried by the supervisor's
// tests.
func carrierProbe() string { return "unsupported" }

// ownGroup: a Windows child's tree is its job's, and no test there asks for
// a group.
func ownGroup(cmd *exec.Cmd) {}
