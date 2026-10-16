//go:build windows

package uninstall

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/aiii-dot-id/aii-os/internal/consolewin"
	"github.com/aiii-dot-id/aii-os/internal/hostcap"
	"github.com/aiii-dot-id/aii-os/internal/install"
	"github.com/aiii-dot-id/aii-os/internal/supervisor"

	"golang.org/x/sys/windows"
)

func processes() ([]Process, error) { return nil, nil }

func sweep() ([]string, error) {
	deleted, err := supervisor.DeleteStaleContainerProfiles()
	var out []string
	for _, d := range deleted {
		out = append(out, "AppContainer profile "+d)
	}
	return out, err
}

func platformStays() []Stay { return nil }

func programPath(exe string) string { return filepath.Dir(exe) }

func removeInstruction(exe string) string {
	return "delete the folder " + programPath(exe) + " (this uninstall cannot remove the folder it runs from)"
}

func ownedHere(string) bool { return true }

var startMenu = func() string {
	return filepath.Join(os.Getenv("APPDATA"), `Microsoft\Windows\Start Menu\Programs`)
}

var shortcutTarget = func(lnk string) (string, error) {
	if cap := hostcap.Can(hostcap.Subprocess); !cap.Available {
		return "", fmt.Errorf("cannot read the shortcut here: %s", cap.Reason)
	}

	c := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command",
		"[Console]::OutputEncoding = [Text.Encoding]::UTF8; (New-Object -ComObject WScript.Shell).CreateShortcut($env:AII_SHORTCUT).TargetPath")
	c.Env = append(os.Environ(), "AII_SHORTCUT="+lnk)
	consolewin.Hide(c)
	out, err := c.Output()
	if err != nil {
		return "", fmt.Errorf("read %s: %w", lnk, err)
	}
	return strings.TrimSpace(strings.TrimPrefix(string(out), "\uFEFF")), nil
}

func shortcuts(programs []install.InstalledProgram) ([]string, []Stay) {
	lnk := filepath.Join(startMenu(), "AII OS.lnk")
	if _, err := os.Lstat(lnk); err != nil {
		return nil, nil
	}
	target, err := shortcutTarget(lnk)
	if err != nil {
		return nil, []Stay{{What: StayOther, Path: lnk, Why: "its target could not be read, so it is not known to be this install's: " + err.Error()}}
	}
	for _, p := range programs {
		if inside(target, filepath.Dir(p.Exe)) {
			if err := os.Remove(lnk); err != nil {
				return nil, []Stay{{What: StayOther, Path: lnk, Why: "it was not removed: " + err.Error()}}
			}
			return []string{lnk}, nil
		}
	}
	return nil, []Stay{{What: StayOther, Path: lnk, Why: "it points at " + target + ", not into this install"}}
}

func inside(path, dir string) bool {
	rel, err := filepath.Rel(strings.ToLower(longName(filepath.Clean(dir))), strings.ToLower(longName(filepath.Clean(path))))
	return err == nil && rel != "." && !strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel)
}

func longName(p string) string {
	u, err := windows.UTF16PtrFromString(p)
	if err != nil {
		return p
	}
	buf := make([]uint16, windows.MAX_LONG_PATH)
	n, err := windows.GetLongPathName(u, &buf[0], uint32(len(buf)))
	if err != nil || n == 0 || int(n) > len(buf) {
		return p
	}
	return windows.UTF16ToString(buf[:n])
}
