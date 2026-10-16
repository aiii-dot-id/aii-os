//go:build windows

package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/aiii-dot-id/aii-os/internal/consolewin"
	"github.com/aiii-dot-id/aii-os/internal/hostcap"
	"github.com/aiii-dot-id/aii-os/internal/install"
)

func main() {
	startup := flag.Bool("startup", false, "started by Windows at sign-in: run the identity, do not open a browser")
	dir := flag.String("dir", "", "identity slot to run (default: the first one)")
	machineMode := flag.Bool("machine", false, "started by Windows at sign-in: run the account's machine service")
	flag.Parse()

	if *machineMode {

		if err := runMachine(); err != nil {
			alert(err.Error())
			os.Exit(1)
		}
		return
	}
	if err := run(*startup, *dir); err != nil {
		alert(err.Error())
		os.Exit(1)
	}
}

func runMachine() error {
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
	return install.StartMachine(aii)
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
	var n int
	if slotDir == "" {

		var created bool
		n, created, err = firstSlot(root, func() error {

			initCmd := exec.Command(aii, "init")
			consolewin.Hide(initCmd)
			if out, err := initCmd.CombinedOutput(); err != nil {
				return fmt.Errorf("create the first identity slot: %v: %s", err, out)
			}
			return nil
		})
		if err != nil {
			return err
		}
		slotDir = filepath.Join(root, install.SlotName(n))
		if created {

			linked := false
			if err := openWhenServing(slotDir, install.ConfiguredPort(slotDir, n), startup, signedInOpener(aii, slotDir, open, &linked)); err != nil {
				return err
			}
			if !startup && !linked {
				tell("AII OS", firstStartTokenNote(aii, slotDir))
			}
			return nil
		}
	} else {
		n = slotNumber(slotDir)
	}

	linked := false
	return runSlot(aii, slotDir, n, startup, signedInOpener(aii, slotDir, open, &linked), serving)
}

func runSlot(aii, slotDir string, n int, startup bool, openURL func(string) error, portInUse func(int) bool) error {

	if err := reachable(slotDir); err != nil {
		return err
	}
	port := install.ConfiguredPort(slotDir, n)
	url := fmt.Sprintf("http://127.0.0.1:%d", port)

	present, err := install.SlotRunning(slotDir)
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
	_ = boxCommand(boxScript, "AII OS could not start", msg).Run()
}

func tell(title, msg string) {
	fmt.Fprintln(os.Stderr, title+": "+msg)
	_ = boxCommand(boxScript, title, msg).Run()
}

const boxScript = "Add-Type -AssemblyName PresentationFramework; [void][System.Windows.MessageBox]::Show($env:AII_BOX_TEXT, $env:AII_BOX_TITLE)"

func boxCommand(script, title, msg string) *exec.Cmd {
	c := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", script)
	c.Env = append(os.Environ(), "AII_BOX_TITLE="+envText(title), "AII_BOX_TEXT="+envText(msg))
	consolewin.Hide(c)
	return c
}

func envText(s string) string {
	s = strings.ReplaceAll(s, "\x00", "")
	if len(s) <= maxBoxText {
		return s
	}
	cut := maxBoxText - len("…")
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}

const maxBoxText = 8 << 10

const terminalName = "PowerShell"

var launcherShell = install.PowerShell

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\r\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
