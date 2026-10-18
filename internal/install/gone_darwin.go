//go:build darwin && !ios

package install

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
)

func SlotGone(slot string) error { return agentGone(Unit + slot) }

func MachineGone() error { return agentGone(MachineLabel) }

func agentGone(label string) error {
	home, err := operatorHome()
	if err != nil {
		return err
	}
	path := filepath.Join(home, agentDirRel, label+".plist")
	if _, err := os.Lstat(path); err == nil {
		return fmt.Errorf("%s's LaunchAgent is still at %s", label, path)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("look for %s: %w", path, err)
	}
	out, err := run("launchctl", "print", "gui/"+strconv.Itoa(os.Getuid())+"/"+label)
	switch {
	case err == nil:
		return fmt.Errorf("launchd still holds %s", label)
	case exitStatus(err) == launchctlNoSuchService:
		return nil
	default:
		return fmt.Errorf("ask launchd for %s: %v: %s", label, err, out)
	}
}
