package install

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows/registry"

	"github.com/aiii-dot-id/aii-os/internal/hostcap"
)

// .
// .
const Unit = "AII OS "

// .
// .
// .
// .
// .
// .
const runKey = `Software\Microsoft\Windows\CurrentVersion\Run`

// .
// .
func Register(slot string) (started bool, notes []string, err error) {
	if cap := hostcap.Can(hostcap.Subprocess); !cap.Available {
		return false, []string{"cannot register a service here: " + cap.Reason}, nil
	}
	root, err := Root()
	if err != nil {
		return false, nil, err
	}
	dir := filepath.Join(root, slot)

	launcher, err := launcherPath()
	if err != nil {
		return false, nil, err
	}

	k, _, err := registry.CreateKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if err != nil {
		return false, nil, fmt.Errorf("open HKCU\\%s: %w", runKey, err)
	}
	defer k.Close()
	// .
	// .
	cmdline := `"` + launcher + `" --startup --dir "` + dir + `"`
	if err := k.SetStringValue(Unit+slot, cmdline); err != nil {
		return false, nil, fmt.Errorf("write autostart entry: %w", err)
	}

	// .
	// .
	// .
	c := exec.Command(launcher, "--startup", "--dir", dir)
	c.Dir = dir
	if err := StartDetached(c); err != nil {
		return false, notes, fmt.Errorf("start %s: %w", slot, err)
	}
	_ = c.Process.Release()

	notes = append(notes, "it will start again when you sign in; Windows lists it under Task Manager → Startup apps")
	return true, notes, nil
}

// .
// .
// .
func Unregister(slot string) error {
	// .
	// .
	// .
	// .
	// .
	// .
	if err := Stop(slot); err != nil && err != ErrNotRunning {
		return fmt.Errorf("stop %s before unregistering: %w", slot, err)
	}
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if err != nil {
		if err == registry.ErrNotExist {
			return nil
		}
		return fmt.Errorf("open HKCU\\%s: %w", runKey, err)
	}
	defer k.Close()
	if err := k.DeleteValue(Unit + slot); err != nil && err != registry.ErrNotExist {
		return fmt.Errorf("remove autostart entry: %w", err)
	}
	return nil
}

// .
func StartCommand(slot, dir string) string {
	return "aii register " + slot
}

// .
// .
// .
// .
// .
func launcherPath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("locate the running binary: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	dir := filepath.Dir(exe)
	for _, name := range []string{"AII OS.exe", "aii-app.exe"} {
		p := filepath.Join(dir, name)
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	// .
	// .
	// .
	if strings.HasSuffix(strings.ToLower(exe), ".exe") {
		return exe, nil
	}
	return "", fmt.Errorf("no launcher found beside %s", exe)
}
