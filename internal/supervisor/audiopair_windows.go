//go:build windows

package supervisor

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sync"
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
// .
func prepareAudioPair(cmd *exec.Cmd, env []string, extra []*os.File) (*audioPair, []string, error) {
	if len(extra) > 0 {
		// .
		// .
		// .
		return nil, env, errors.New("inherited descriptors are not a Windows mechanism; this spawn asked for both")
	}
	childIn, hostIn, err := os.Pipe()
	if err != nil {
		return nil, env, fmt.Errorf("audio input pipe: %w", err)
	}
	hostOut, childOut, err := os.Pipe()
	if err != nil {
		childIn.Close()
		hostIn.Close()
		return nil, env, fmt.Errorf("audio output pipe: %w", err)
	}
	// .
	inherit, err := inheritableHandles(childIn, childOut)
	childIn.Close()
	childOut.Close()
	if err != nil {
		hostIn.Close()
		hostOut.Close()
		return nil, env, err
	}
	var once sync.Once
	release := func() {
		once.Do(func() {
			for _, h := range inherit {
				_ = syscall.CloseHandle(h)
			}
		})
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	// .
	cmd.SysProcAttr.AdditionalInheritedHandles = append(cmd.SysProcAttr.AdditionalInheritedHandles, inherit...)
	env = append(append([]string{}, env...),
		fmt.Sprintf("AII_AUDIO_IN_FD=%d", uint64(inherit[0])),
		fmt.Sprintf("AII_AUDIO_OUT_FD=%d", uint64(inherit[1])))
	return &audioPair{hostIn: hostIn, hostOut: hostOut, childClose: release}, env, nil
}

// .
// .
// .
func inheritableHandles(ends ...*os.File) ([]syscall.Handle, error) {
	self, err := syscall.GetCurrentProcess()
	if err != nil {
		return nil, fmt.Errorf("audio pair: current process: %w", err)
	}
	var out []syscall.Handle
	for _, f := range ends {
		var dup syscall.Handle
		if err := syscall.DuplicateHandle(self, syscall.Handle(f.Fd()), self, &dup, 0, true, syscall.DUPLICATE_SAME_ACCESS); err != nil {
			for _, h := range out {
				_ = syscall.CloseHandle(h)
			}
			return nil, fmt.Errorf("audio pair: inheritable handle: %w", err)
		}
		out = append(out, dup)
	}
	return out, nil
}
