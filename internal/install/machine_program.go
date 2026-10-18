//go:build (linux && !android) || (darwin && !ios) || windows

package install

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/aiii-dot-id/aii-os/internal/fileperm"
	"github.com/aiii-dot-id/aii-os/internal/version"
)

var ErrNoInstalledProgram = errors.New("this account has no installed AII OS program")

type NotInstalledError struct {
	Running   string
	Installed string
}

func (e *NotInstalledError) Error() string {
	return fmt.Sprintf("the machine service is registered only from this account's installed program (%s); this is %s", e.Installed, e.Running)
}

type InstalledProgram struct {
	Exe     string
	Version string
}

func InstalledPrograms() []InstalledProgram { return installedPrograms() }

func ServiceProgram(running string) (string, error) {
	return serviceProgram(installedPrograms(), running)
}

func serviceProgram(installed []InstalledProgram, running string) (string, error) {
	if len(installed) == 0 {
		return "", ErrNoInstalledProgram
	}
	best := installed[0]
	for _, p := range installed[1:] {
		switch {
		case best.Version == "" && p.Version != "":
			best = p
		case version.Valid(best.Version) && version.Valid(p.Version) && version.Compare(p.Version, best.Version) > 0:
			best = p
		}
	}
	if !samePath(best.Exe, running) {
		return "", &NotInstalledError{Running: running, Installed: best.Exe}
	}
	return best.Exe, nil
}

func samePath(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	fa, errA := os.Stat(a)
	fb, errB := os.Stat(b)
	if errA != nil || errB != nil {
		return filepath.Clean(a) == filepath.Clean(b)
	}
	return os.SameFile(fa, fb)
}

func RetireMachineIfLast() (bool, error) {
	slots, err := StartupSlots()
	if err != nil {
		return false, fmt.Errorf("read this account's startup registrations: %w", err)
	}
	if len(slots) > 0 {
		return false, nil
	}
	if _, registered, err := MachineRegistration(); err != nil || !registered {
		return false, err
	}
	if err := UnregisterMachine(); err != nil {
		return false, err
	}
	return true, nil
}

func ensureMachineDir(dir string) error {
	if _, err := os.Stat(dir); err == nil {
		return nil
	}
	if err := fileperm.MkdirOwnerOnly(dir); err != nil && !errors.Is(err, os.ErrExist) {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	return nil
}
