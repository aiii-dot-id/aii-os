//go:build windows

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

	"github.com/aiii-dot-id/aii-os/internal/consolewin"
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

			initCmd := exec.Command(aii, "init")
			consolewin.Hide(initCmd)
			out, err := initCmd.CombinedOutput()
			if err != nil {
				return fmt.Errorf("create the first identity slot: %v: %s", err, out)
			}

			slots, err = install.Slots(root)
			if err != nil || len(slots) == 0 {
				return fmt.Errorf("the slot was created but cannot be found again")
			}
			n = slots[0]
			d := filepath.Join(root, install.SlotName(n))
			return openWhenServing(d, install.ConfiguredPort(d, n), startup, open)
		}
		n = slots[0]
		slotDir = filepath.Join(root, install.SlotName(n))
	} else {
		n = slotNumber(slotDir)
	}

	return runSlot(aii, slotDir, n, startup, open, serving)
}

func runSlot(aii, slotDir string, n int, startup bool, openURL func(string) error, portInUse func(int) bool) error {
	port := install.ConfiguredPort(slotDir, n)
	url := fmt.Sprintf("http://127.0.0.1:%d", port)

	present, err := install.StopChannelPresent(slotDir)
	if err != nil {
		return fmt.Errorf("cannot determine whether this identity is running: %w", err)
	}
	if present {
		if startup {
			return nil
		}
		return openURL(url)
	}
	if portInUse(port) {
		return fmt.Errorf("port %d is already in use, but its process could not be identified as this identity.\n\nNo second process was started and the browser was not opened. Check the process on that port and the dashboard port in %s before changing settings.",
			port, install.ConfigPathIn(slotDir))
	}

	if err := os.MkdirAll(filepath.Join(slotDir, "log"), 0o700); err != nil {
		return fmt.Errorf("prepare the log directory: %w", err)
	}
	bootLog := filepath.Join(slotDir, "log", "boot.log")
	lf, err := os.OpenFile(bootLog, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open %s: %w", bootLog, err)
	}
	defer lf.Close()

	p, err := install.StartDetached(func() *exec.Cmd {
		cmd := exec.Command(aii, "-dir", slotDir)
		cmd.Dir = slotDir
		cmd.Stdout, cmd.Stderr = lf, lf
		return cmd
	})
	if err != nil {
		return fmt.Errorf("start the identity: %w", err)
	}
	_ = p.Release()

	return openWhenServing(slotDir, port, startup, openURL)
}

func openWhenServing(slotDir string, port int, startup bool, openURL func(string) error) error {
	url := fmt.Sprintf("http://127.0.0.1:%d", port)
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		if serving(port) {
			if startup {
				return nil
			}
			return openURL(url)
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

func open(url string) error {
	return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
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

func alert(msg string) {
	fmt.Fprintln(os.Stderr, msg)
	script := "Add-Type -AssemblyName PresentationFramework; " +
		"[System.Windows.MessageBox]::Show(" + psQuote(msg) + ", 'AII OS could not start')"

	c := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", script)
	consolewin.Hide(c)
	_ = c.Run()
}

func psQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\r\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
