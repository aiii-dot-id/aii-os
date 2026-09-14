//go:build !windows

package supervisor

import (
	"fmt"
	"os"
	"os/exec"
)

// .
// .
// .
// .
func prepareAudioPair(cmd *exec.Cmd, env []string, extra []*os.File) (*audioPair, []string, error) {
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
	base := 3 + len(extra)
	cmd.ExtraFiles = append(append([]*os.File{}, extra...), childIn, childOut)
	env = append(append([]string{}, env...),
		fmt.Sprintf("AII_AUDIO_IN_FD=%d", base), fmt.Sprintf("AII_AUDIO_OUT_FD=%d", base+1))
	return &audioPair{hostIn: hostIn, hostOut: hostOut, childClose: func() {
		childIn.Close()
		childOut.Close()
	}}, env, nil
}
