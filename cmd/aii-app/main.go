//go:build (darwin && !ios) || (linux && !android)

package main

import (
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/hostcap"
	"github.com/aiii-dot-id/aii-os/internal/install"
)

func main() {
	if err := run(); err != nil {
		if errors.Is(err, errPortKept) {

			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}

		alert(err.Error())
		os.Exit(1)
	}
}

func run() error {

	if cap := hostcap.Can(hostcap.Subprocess); !cap.Available {
		return fmt.Errorf("this bundle cannot start anything here: %s", cap.Reason)
	}
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate the bundle: %w", err)
	}
	aii := filepath.Join(filepath.Dir(exe), "aii")
	if _, err := os.Stat(aii); err != nil {
		return fmt.Errorf("the aii binary is missing from this bundle: %w", err)
	}

	root, err := install.Root()
	if err != nil {
		return err
	}
	slots, err := install.Slots(root)
	if err != nil {
		return err
	}

	n := 0
	if len(slots) == 0 {

		out, err := exec.Command(aii, "init").CombinedOutput()
		if err != nil {
			return fmt.Errorf("create the first identity slot: %v: %s", err, out)
		}
	} else {
		n = slots[0]
	}

	dir := filepath.Join(root, install.SlotName(n))

	port := install.ConfiguredPort(dir, n)

	url := fmt.Sprintf("http://127.0.0.1:%d", port)

	if running(dir) {

		return waitServing(port, url, dir, "", nil)
	}
	if serving(port) {

		if port, err = movePort(root, dir, port, ask, writePort(aii, dir), portFree); err != nil {
			return err
		}
		url = fmt.Sprintf("http://127.0.0.1:%d", port)
	}

	if startService(install.SlotName(n)) {
		return waitServing(port, url, dir, "", nil)
	}

	if err := os.MkdirAll(filepath.Join(dir, "log"), 0o700); err != nil {
		return fmt.Errorf("prepare the log directory: %w", err)
	}
	bootLog := filepath.Join(dir, "log", "boot.log")
	lf, err := os.OpenFile(bootLog, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open %s: %w", bootLog, err)
	}
	defer lf.Close()

	cmd := exec.Command(aii, "-dir", dir)
	cmd.Dir = dir
	cmd.Stdout, cmd.Stderr = lf, lf
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start the identity: %w", err)
	}

	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	return waitServing(port, url, dir, bootLog, exited)
}

func waitServing(port int, url, dir, bootLog string, exited <-chan error) error {
	deadline := time.After(60 * time.Second)
	for {
		select {
		case <-exited:

			raw, _ := os.ReadFile(bootLog)
			msg := strings.TrimSpace(lastLines(string(raw), 12))
			if msg == "" {
				msg = "it exited without saying why; see " + filepath.Join(dir, "log", "aii.log")
			}
			return fmt.Errorf("the identity stopped during startup:\n\n%s", msg)
		case <-deadline:
			return fmt.Errorf("the identity did not start serving on port %d within 60s — see %s", port, filepath.Join(dir, "log", "aii.log"))
		default:
			if serving(port) {
				return openURL(url)
			}
			time.Sleep(250 * time.Millisecond)
		}
	}
}

func writePort(aii, dir string) func(port int) error {
	return func(port int) error {
		out, err := exec.Command(aii, "dashboard-port", "-dir", dir, strconv.Itoa(port)).CombinedOutput()
		if err != nil {
			return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
		}
		return nil
	}
}

func running(slotDir string) bool {
	out, err := exec.Command("pgrep", "-f", "aii -dir "+slotDir).Output()
	return err == nil && len(strings.TrimSpace(string(out))) > 0
}

func serving(port int) bool {
	c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 300*time.Millisecond)
	if err != nil {
		return false
	}
	c.Close()
	return true
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
