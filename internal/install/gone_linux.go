//go:build linux && !android

package install

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

func SlotGone(slot string) error {
	unit := Unit + slot + ".service"
	link, err := wantsLink(unit)
	if err != nil {
		return err
	}
	if err := absentPath(link, unit+"'s enable link"); err != nil {
		return err
	}
	return unitStopped(unit)
}

func MachineGone() error {
	dir, err := userUnitDir()
	if err != nil {
		return err
	}
	for _, name := range []string{MachineUnit, MachineSocket} {
		if err := absentPath(filepath.Join(dir, name), name); err != nil {
			return err
		}
		link, err := wantsLink(name)
		if err != nil {
			return err
		}
		if err := absentPath(link, name+"'s enable link"); err != nil {
			return err
		}
		if err := unitStopped(name); err != nil {
			return err
		}
	}
	return nil
}

func absentPath(path, what string) error {
	if _, err := os.Lstat(path); err == nil {
		return fmt.Errorf("%s is still at %s", what, path)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("look for %s: %w", path, err)
	}
	return nil
}

func unitStopped(unit string) error {
	out, err := run("systemctl", "--user", "is-active", unit)
	lines := strings.Split(strings.TrimSpace(out), "\n")
	switch state := strings.TrimSpace(lines[len(lines)-1]); state {
	case "inactive", "failed":
		return nil
	case "active", "activating", "deactivating", "reloading", "refreshing":
		return fmt.Errorf("%s is %s", unit, state)
	}
	if err != nil {
		return fmt.Errorf("ask the user manager about %s: %v: %s", unit, err, out)
	}
	return fmt.Errorf("the user manager says %s is %q", unit, out)
}
