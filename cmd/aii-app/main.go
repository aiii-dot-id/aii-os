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
	return launch(aii)
}

func launch(aii string) error {
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

	present, err := slotRunning(dir)
	if err != nil {

		return fmt.Errorf("cannot determine whether this identity is running: %w", err)
	}
	if present {

		return waitServing(port, url, dir)
	}
	if serving(port) {

		if port, err = movePort(root, dir, port, ask, writePort(aii, dir), portFree); err != nil {
			return err
		}
		url = fmt.Sprintf("http://127.0.0.1:%d", port)
	}

	slot := install.SlotName(n)
	if err := startService(aii, slot); err != nil {
		return fmt.Errorf("the service manager did not start %s: %w", slot, err)
	}
	return waitServing(port, url, dir)
}

func startService(aii, slot string) error {
	out, err := exec.Command(aii, "register", slot).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func waitServing(port int, url, dir string) error {
	deadline := time.After(60 * time.Second)
	for {
		select {
		case <-deadline:
			return fmt.Errorf("the identity did not start serving on port %d within 60s — see %s", port, filepath.Join(dir, "log", "aii.log"))
		default:

			if serving(port) {
				present, err := slotRunning(dir)
				if err != nil {
					return fmt.Errorf("cannot determine whether this identity is running: %w", err)
				}
				if present {
					return openURL(url)
				}
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

func serving(port int) bool {
	c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 300*time.Millisecond)
	if err != nil {
		return false
	}
	c.Close()
	return true
}
