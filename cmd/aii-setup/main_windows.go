//go:build windows

package main

import (
	"embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/aiii-dot-id/aii-os/internal/consolewin"
	"github.com/aiii-dot-id/aii-os/internal/hostcap"
	"github.com/aiii-dot-id/aii-os/internal/install"
)

//go:embed payload
var payload embed.FS

const appDirName = `Programs\AII OS`

func main() {
	if err := runInstall(); err != nil {
		alert("Setup could not finish.\n\n" + err.Error())
		os.Exit(1)
	}
}

func runInstall() error {

	if cap := hostcap.Can(hostcap.Subprocess); !cap.Available {
		return fmt.Errorf("this installer cannot run here: %s", cap.Reason)
	}
	local := os.Getenv("LOCALAPPDATA")
	if local == "" {
		return fmt.Errorf("LOCALAPPDATA is not set, so there is nowhere per-user to install")
	}
	dest := filepath.Join(local, appDirName)
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", dest, err)
	}

	stopOurProcesses(dest)

	entries, err := payload.ReadDir("payload")
	if err != nil {
		return fmt.Errorf("read payload: %w", err)
	}
	installed := 0
	for _, e := range entries {

		isExe := strings.HasSuffix(strings.ToLower(e.Name()), ".exe")
		if e.IsDir() || (!isExe && e.Name() != "LICENSE") {
			continue
		}
		b, err := payload.ReadFile("payload/" + e.Name())
		if err != nil {
			return fmt.Errorf("read %s: %w", e.Name(), err)
		}
		out := filepath.Join(dest, e.Name())
		mode := os.FileMode(0o644)
		if isExe {
			mode = 0o755
		}
		if err := os.WriteFile(out, b, mode); err != nil {
			return fmt.Errorf("write %s: %w", out, err)
		}
		if isExe {
			installed++
		}
	}
	if installed == 0 {
		return fmt.Errorf("this installer carries no program — it was built without its payload")
	}

	launcher := filepath.Join(dest, "AII OS.exe")
	if err := shortcut(startMenuDir(), "AII OS", launcher, dest); err != nil {

		alert("AII OS is installed, but the Start Menu shortcut could not be created:\n\n" + err.Error())
	}

	p, err := install.StartDetached(func() *exec.Cmd {
		c := exec.Command(launcher)
		c.Dir = dest
		return c
	})
	if err != nil {
		return fmt.Errorf("start AII OS: %w", err)
	}
	_ = p.Release()
	return nil
}

func startMenuDir() string {
	return filepath.Join(os.Getenv("APPDATA"), `Microsoft\Windows\Start Menu\Programs`)
}

func shortcut(dir, name, target, workdir string) error {
	if dir == "" {
		return fmt.Errorf("no Start Menu directory")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	lnk := filepath.Join(dir, name+".lnk")
	ps := strings.Join([]string{
		"$s = (New-Object -ComObject WScript.Shell).CreateShortcut(" + psQuote(lnk) + ");",
		"$s.TargetPath = " + psQuote(target) + ";",
		"$s.WorkingDirectory = " + psQuote(workdir) + ";",
		"$s.Description = 'AII OS';",
		"$s.Save()",
	}, " ")
	c := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", ps)
	consolewin.Hide(c)
	out, err := c.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func alert(msg string) {
	fmt.Fprintln(os.Stderr, msg)
	script := "Add-Type -AssemblyName PresentationFramework; " +
		"[System.Windows.MessageBox]::Show(" + psQuote(msg) + ", 'AII OS Setup')"
	c := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", script)
	consolewin.Hide(c)
	_ = c.Run()
}

func psQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
