//go:build darwin && !ios

package uninstall

import (
	"bufio"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/aiii-dot-id/aii-os/internal/hostcap"
	"github.com/aiii-dot-id/aii-os/internal/install"
)

func processes() ([]Process, error) {
	if cap := hostcap.Can(hostcap.Subprocess); !cap.Available {
		return nil, fmt.Errorf("cannot read the process table here: %s", cap.Reason)
	}
	out, err := exec.Command("/bin/ps", "-axo", "uid=,comm=").Output()
	if err != nil {
		return nil, fmt.Errorf("read the process table: %w", err)
	}
	return parsePS(string(out)), nil
}

func parsePS(out string) []Process {
	var procs []Process
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		uid, exe, ok := strings.Cut(strings.TrimSpace(sc.Text()), " ")
		if !ok {
			continue
		}
		n, err := strconv.Atoi(uid)
		if err != nil {
			continue
		}
		procs = append(procs, Process{UID: n, Exe: strings.TrimSpace(exe)})
	}
	return procs
}

func sweep() ([]string, error) { return nil, nil }

func platformStays() []Stay { return nil }

func programPath(exe string) string {
	macOS := filepath.Dir(exe)
	if app := filepath.Dir(filepath.Dir(macOS)); strings.HasSuffix(app, ".app") {
		return app
	}
	return exe
}

func removeInstruction(exe string) string {
	return "drag " + programPath(exe) + " to the Trash"
}

func shortcuts([]install.InstalledProgram) ([]string, []Stay) { return nil, nil }
