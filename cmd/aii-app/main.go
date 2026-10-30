//go:build (darwin && !ios) || (linux && !android)

package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/aiii-dot-id/aii-os/internal/hostcap"
	"github.com/aiii-dot-id/aii-os/internal/install"
)

var launcherShell = install.POSIX

var slotRunning = install.SlotRunning

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

	if slices.Contains(os.Args[1:], "--uninstall") {
		return uninstallFromApp(aii)
	}
	return launch(aii)
}

func launch(aii string) error {
	root, err := install.Root()
	if err != nil {
		return err
	}

	n, created, err := firstSlot(root, func() error {

		out, err := exec.Command(aii, "init").CombinedOutput()
		if started := notServing(err, out); started != nil {
			return started
		}
		if err != nil {
			return fmt.Errorf("create the first identity slot: %v: %s", err, out)
		}
		return nil
	})
	if err != nil {
		return err
	}

	dir := filepath.Join(root, install.SlotName(n))

	linked := false
	open := signedInOpener(aii, dir, openURL, &linked)
	opened := func(err error) error {
		if err == nil && created && !linked {
			tell("AII OS", firstStartTokenNote(aii, dir))
		}
		return err
	}

	if err := reachable(dir); err != nil {
		return err
	}

	port := install.ConfiguredPort(dir, n)

	url := fmt.Sprintf("http://127.0.0.1:%d", port)

	present, err := slotRunning(dir)
	if err != nil {

		return fmt.Errorf("cannot determine whether this identity is running: %w", err)
	}
	if present {

		return opened(waitServing(port, url, dir, open))
	}
	if install.Serving(port) {

		if port, err = movePort(root, dir, port, ask, writePort(aii, dir), install.PortFree); err != nil {
			return err
		}
		url = fmt.Sprintf("http://127.0.0.1:%d", port)
	}

	slot := install.SlotName(n)
	if err := startService(aii, slot); err != nil {
		var started *startedNotServing
		if errors.As(err, &started) {
			return err
		}
		return fmt.Errorf("the service manager did not start %s: %w", slot, err)
	}
	return opened(waitServing(port, url, dir, open))
}

type startedNotServing struct{ words string }

func (e *startedNotServing) Error() string { return e.words }

func notServing(err error, out []byte) error {
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == install.ExitNotServing {
		return &startedNotServing{words: strings.TrimSpace(string(out))}
	}
	return nil
}

func startService(aii, slot string) error {
	out, err := exec.Command(aii, "register", slot).CombinedOutput()
	if started := notServing(err, out); started != nil {
		return started
	}
	if err != nil {
		return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func waitServing(port int, url, dir string, open func(string) error) error {
	err := install.WaitServing(port, dir, install.ConfiguredStartWait(dir))
	var ns *install.NotServingError
	if errors.As(err, &ns) {
		return fmt.Errorf("%v — see %s", err, filepath.Join(dir, "log", "aii.log"))
	}
	if err != nil {
		return err
	}
	return open(url)
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
