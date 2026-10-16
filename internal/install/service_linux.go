//go:build linux && !android

package install

import (
	"fmt"
	"os"

	"github.com/aiii-dot-id/aii-os/internal/hostcap"
)

const Unit = "aii-os@"

func Register(slot string) (started bool, notes []string, err error) {

	if cap := hostcap.Can(hostcap.Subprocess); !cap.Available {
		return false, []string{"cannot register a service here: " + cap.Reason}, nil
	}
	if err := OwnAccount(); err != nil {
		return false, nil, err
	}
	unit := Unit + slot + ".service"

	if out, lerr := run("loginctl", "enable-linger"); lerr != nil {
		notes = append(notes, fmt.Sprintf("could not enable lingering (%v: %s) — the identity will stop when you log out; run: sudo loginctl enable-linger $USER", lerr, out))
	}

	if out, err := run("systemctl", "--user", "enable", "--now", unit); err != nil {
		return false, notes, fmt.Errorf("enable %s: %v: %s", unit, err, out)
	}
	return true, notes, nil
}

func Unregister(slot string) error {
	if cap := hostcap.Can(hostcap.Subprocess); !cap.Available {
		return fmt.Errorf("cannot unregister a service here: %s", cap.Reason)
	}
	if err := OwnAccount(); err != nil {
		return err
	}
	unit := Unit + slot + ".service"
	out, err := run("systemctl", "--user", "disable", "--now", unit)
	if err == nil {
		return nil
	}
	if link, lerr := wantsLink(unit); lerr == nil {
		if fi, serr := os.Lstat(link); serr == nil && fi.Mode()&os.ModeSymlink != 0 {
			_ = os.Remove(link)
		}
	}
	_, _ = run("systemctl", "--user", "stop", unit)
	if gerr := SlotGone(slot); gerr != nil {
		return fmt.Errorf("disable %s: %v: %s (%w)", unit, err, out, gerr)
	}
	return nil
}

func StartCommand(slot, dir string) string {
	return "systemctl --user enable --now " + Unit + slot + ".service"
}

func Stop(slot string) error {
	if cap := hostcap.Can(hostcap.Subprocess); !cap.Available {
		return fmt.Errorf("cannot stop a service here: %s", cap.Reason)
	}
	if err := OwnAccount(); err != nil {
		return err
	}
	unit := Unit + slot + ".service"
	if out, err := run("systemctl", "--user", "stop", unit); err != nil {
		return fmt.Errorf("stop %s: %v: %s", unit, err, out)
	}
	return nil
}
