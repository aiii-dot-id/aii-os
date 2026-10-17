//go:build windows

package uninstall

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/install"
)

// makeShortcut writes a .lnk pointing at target, as the installer does
// (WScript.Shell), the paths handed over in the environment.
func makeShortcut(t *testing.T, lnk, target string) {
	t.Helper()
	c := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command",
		"$s = (New-Object -ComObject WScript.Shell).CreateShortcut($env:AII_SHORTCUT); $s.TargetPath = $env:AII_SHORTCUT_TARGET; $s.Save()")
	c.Env = append(os.Environ(), "AII_SHORTCUT="+lnk, "AII_SHORTCUT_TARGET="+target)
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("make the shortcut: %v: %s", err, out)
	}
}

func TestTheShortcutGoesOnlyWhenItPointsIntoTheInstall(t *testing.T) {
	menu := t.TempDir()
	saved := startMenu
	startMenu = func() string { return menu }
	t.Cleanup(func() { startMenu = saved })
	installDir := filepath.Join(t.TempDir(), "Programs", "AII OS")
	if err := os.MkdirAll(installDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"aii.exe", "AII OS.exe"} {
		if err := os.WriteFile(filepath.Join(installDir, f), []byte("x"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	programs := []install.InstalledProgram{{Exe: filepath.Join(installDir, "aii.exe")}}
	lnk := filepath.Join(menu, "AII OS.lnk")

	elsewhere := filepath.Join(t.TempDir(), "someone else's.exe")
	if err := os.WriteFile(elsewhere, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	makeShortcut(t, lnk, elsewhere)
	removed, stays := shortcuts(programs)
	if len(removed) != 0 || len(stays) != 1 || !strings.Contains(stays[0].Why, "not into this install") {
		t.Fatalf("a shortcut pointing elsewhere: removed %v, stays %+v", removed, stays)
	}
	if _, err := os.Stat(lnk); err != nil {
		t.Fatalf("a shortcut not this install's was removed: %v", err)
	}

	makeShortcut(t, lnk, filepath.Join(installDir, "AII OS.exe"))
	removed, stays = shortcuts(programs)
	if len(removed) != 1 || len(stays) != 0 {
		t.Fatalf("the install's shortcut: removed %v, stays %+v", removed, stays)
	}
	if _, err := os.Stat(lnk); !os.IsNotExist(err) {
		t.Fatalf("the install's shortcut stayed: %v", err)
	}
	if removed, stays := shortcuts(programs); len(removed) != 0 || len(stays) != 0 {
		t.Fatalf("no shortcut at all: removed %v, stays %+v", removed, stays)
	}
}

func TestAShortcutIsReadWhateverQuotesItsPathHolds(t *testing.T) {
	t.Chdir(t.TempDir()) // where a command run from a name would write its file
	menu := filepath.Join(t.TempDir(), "John’s menu ’+(Set-Content aii-probe.txt x)+’ it's")
	if err := os.MkdirAll(menu, 0o755); err != nil {
		t.Fatal(err)
	}
	saved := startMenu
	startMenu = func() string { return menu }
	t.Cleanup(func() { startMenu = saved })
	installDir := filepath.Join(t.TempDir(), "Programs", "John’s AII OS")
	if err := os.MkdirAll(installDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"aii.exe", "AII OS.exe"} {
		if err := os.WriteFile(filepath.Join(installDir, f), []byte("x"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	lnk := filepath.Join(menu, "AII OS.lnk")
	makeShortcut(t, lnk, filepath.Join(installDir, "AII OS.exe"))
	removed, stays := shortcuts([]install.InstalledProgram{{Exe: filepath.Join(installDir, "aii.exe")}})
	if len(removed) != 1 || len(stays) != 0 {
		t.Fatalf("the install's shortcut under a quoted folder: removed %v, stays %+v", removed, stays)
	}
	if _, err := os.Stat("aii-probe.txt"); err == nil {
		t.Fatal("a path ran as PowerShell: its command wrote aii-probe.txt")
	}
}
