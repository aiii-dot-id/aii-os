//go:build linux && !android

package tools

import (
	"os"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

func shellProcess(t *testing.T, st *ShellTool, pid int) (int, bool) {
	t.Helper()
	if shellAccountRoot() {
		return pid, syscall.Kill(pid, 0) != syscall.ESRCH
	}
	s := st.holder.state
	if s == nil {
		t.Fatal("the shell ran with no sandbox: there is no process table to find its processes in")
	}
	s.mu.Lock()
	h := s.cur
	s.mu.Unlock()
	if h == nil || !h.alive() {
		t.Fatal("the shell's sandbox is not up, so whether its process is running cannot be read")
	}
	entries, err := os.ReadDir("/proc")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		host, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		if ns, err := os.Readlink("/proc/" + e.Name() + "/ns/pid"); err != nil || ns != h.pidNS {
			continue
		}
		status, err := os.ReadFile("/proc/" + e.Name() + "/status")
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(status), "\n") {
			if f := strings.Fields(strings.TrimPrefix(line, "NSpid:")); strings.HasPrefix(line, "NSpid:") && len(f) > 0 && f[len(f)-1] == strconv.Itoa(pid) {
				if state := procState(status); state == "Z" || state == "X" {
					return host, false
				}
				return host, syscall.Kill(host, 0) == nil
			}
		}
	}
	return 0, false
}

// procState is the one letter /proc/<pid>/status gives a process's state.
func procState(status []byte) string {
	for _, line := range strings.Split(string(status), "\n") {
		if f := strings.Fields(strings.TrimPrefix(line, "State:")); strings.HasPrefix(line, "State:") && len(f) > 0 {
			return f[0]
		}
	}
	return ""
}
