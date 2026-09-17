//go:build !windows

package supervisor

import (
	"errors"
	"io"
	"os/exec"
)

// .
// .
// .
// .
type launched struct {
	stdin          io.WriteCloser
	stdout, stderr io.ReadCloser
	contained      func() error
	containment    Containment
}

func launchContained(*exec.Cmd, *AppContainer, uint64) (*launched, error) {
	return nil, errors.New("the AppContainer is a Windows mechanism")
}
