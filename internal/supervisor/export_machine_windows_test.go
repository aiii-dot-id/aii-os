//go:build windows

package supervisor

import (
	"io"
	"os/exec"
)

// What the external tests (package supervisor_test) reach of this
// package's own wall tests: an external test may import a package that
// imports this one, as the machine service does.

// BuildWallProbeForTest builds the wall probe.
var BuildWallProbeForTest = buildWallProbe

// LaunchedForTest is a contained launch's pipes and its end's check.
type LaunchedForTest struct {
	Stdin          io.WriteCloser
	Stdout, Stderr io.ReadCloser
	Contained      func() error
}

// LaunchContainedForTest launches cmd inside the AppContainer.
func LaunchContainedForTest(cmd *exec.Cmd, ac *AppContainer) (*LaunchedForTest, error) {
	l, err := launchContained(cmd, ac, 0)
	if err != nil {
		return nil, err
	}
	return &LaunchedForTest{Stdin: l.stdin, Stdout: l.stdout, Stderr: l.stderr, Contained: l.contained}, nil
}

// ReadAllStringForTest reads r whole.
var ReadAllStringForTest = readAllString
