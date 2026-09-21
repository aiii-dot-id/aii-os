//go:build windows

// .
// .
// .
// .
// .
// .
// .
// .
// .
package main

import (
	"flag"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/hostcap"
	"github.com/aiii-dot-id/aii-os/internal/install"
)

func main() {
	startup := flag.Bool("startup", false, "started by Windows at sign-in: run the identity, do not open a browser")
	dir := flag.String("dir", "", "identity slot to run (default: the first one)")
	flag.Parse()

	if err := run(*startup, *dir); err != nil {
		alert(err.Error())
		os.Exit(1)
	}
}

func run(startup bool, slotDir string) error {
	if cap := hostcap.Can(hostcap.Subprocess); !cap.Available {
		return fmt.Errorf("this launcher cannot start anything here: %s", cap.Reason)
	}
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate the bundle: %w", err)
	}
	aii := filepath.Join(filepath.Dir(exe), "aii.exe")
	if _, err := os.Stat(aii); err != nil {
		return fmt.Errorf("aii.exe is missing from this installation: %w", err)
	}

	root, err := install.Root()
	if err != nil {
		return err
	}
	n := 0
	if slotDir == "" {
		slots, err := install.Slots(root)
		if err != nil {
			return err
		}
		if len(slots) == 0 {
			// .
			// .
			// .
			out, err := exec.Command(aii, "init").CombinedOutput()
			if err != nil {
				return fmt.Errorf("create the first identity slot: %v: %s", err, out)
			}
			// .
			slots, err = install.Slots(root)
			if err != nil || len(slots) == 0 {
				return fmt.Errorf("the slot was created but cannot be found again")
			}
			n = slots[0]
			d := filepath.Join(root, install.SlotName(n))
			return openWhenServing(d, install.ConfiguredPort(d, n), startup)
		}
		n = slots[0]
		slotDir = filepath.Join(root, install.SlotName(n))
	} else {
		n = slotNumber(slotDir)
	}

	port := install.ConfiguredPort(slotDir, n)
	url := fmt.Sprintf("http://127.0.0.1:%d", port)

	// .
	// .
	// .
	// .
	if running(slotDir) {
		if startup {
			return nil
		}
		return open(url)
	}
	if serving(port) {
		return fmt.Errorf("port %d is already in use by another program.\n\nThis identity is not running, so opening that address would show you something else. Change \"dashboard\".\"port\" in %s and try again.",
			port, install.ConfigPathIn(slotDir))
	}

	// .
	// .
	// .
	if err := os.MkdirAll(filepath.Join(slotDir, "log"), 0o700); err != nil {
		return fmt.Errorf("prepare the log directory: %w", err)
	}
	bootLog := filepath.Join(slotDir, "log", "boot.log")
	lf, err := os.OpenFile(bootLog, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open %s: %w", bootLog, err)
	}
	defer lf.Close()

	cmd := exec.Command(aii, "-dir", slotDir)
	cmd.Dir = slotDir
	cmd.Stdout, cmd.Stderr = lf, lf
	if err := install.StartDetached(cmd); err != nil {
		return fmt.Errorf("start the identity: %w", err)
	}
	_ = cmd.Process.Release()

	return openWhenServing(slotDir, port, startup)
}

func openWhenServing(slotDir string, port int, startup bool) error {
	url := fmt.Sprintf("http://127.0.0.1:%d", port)
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		if serving(port) {
			if startup {
				return nil
			}
			return open(url)
		}
		time.Sleep(250 * time.Millisecond)
	}
	raw, _ := os.ReadFile(filepath.Join(slotDir, "log", "boot.log"))
	msg := strings.TrimSpace(lastLines(string(raw), 12))
	if msg == "" {
		msg = "see " + filepath.Join(slotDir, "log", "aii.log")
	}
	return fmt.Errorf("the identity did not start serving on port %d within 60s:\n\n%s", port, msg)
}

// .
// .
func open(url string) error {
	return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
}

func running(slotDir string) bool {
	// .
	// .
	// .
	out, err := exec.Command("cmd", "/c",
		`wmic process where "name='aii.exe'" get commandline /value`).Output()
	if err != nil {
		return false
	}
	return strings.Contains(strings.ToLower(string(out)), strings.ToLower(slotDir))
}

func serving(port int) bool {
	c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 300*time.Millisecond)
	if err != nil {
		return false
	}
	c.Close()
	return true
}

func slotNumber(dir string) int {
	base := filepath.Base(dir)
	n := 0
	if _, err := fmt.Sscanf(base, "identity-%d", &n); err != nil {
		return 0
	}
	return n
}

// .
// .
func alert(msg string) {
	fmt.Fprintln(os.Stderr, msg)
	script := "Add-Type -AssemblyName PresentationFramework; " +
		"[System.Windows.MessageBox]::Show(" + psQuote(msg) + ", 'AII OS could not start')"
	_ = exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", script).Run()
}

func psQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\r\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
