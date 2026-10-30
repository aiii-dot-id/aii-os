//go:build linux && !android

package app

import (
	"os"
	"strconv"
	"strings"
	"testing"
)

// leftJobGroup is the process group of the job a command left, as this
// host numbers it. Behind the shell's wall on Linux the command numbers
// processes in its sandbox's own table, so the shell's $$ is not this
// host's number; the job is found here instead by the marker its shell
// was started with, and its group read from this host's /proc. Run as
// root the shell is not walled, and the marker finds the job the same way.
func leftJobGroup(t *testing.T, _ string, marker string) int {
	t.Helper()
	entries, err := os.ReadDir("/proc")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if _, err := strconv.Atoi(e.Name()); err != nil {
			continue
		}
		cmdline, err := os.ReadFile("/proc/" + e.Name() + "/cmdline")
		if err != nil || !strings.Contains(string(cmdline), marker) {
			continue
		}
		stat, err := os.ReadFile("/proc/" + e.Name() + "/stat")
		if err != nil {
			continue
		}
		// pid (comm) state ppid pgrp ...: the fields after the last ")".
		fields := strings.Fields(string(stat[strings.LastIndexByte(string(stat), ')')+1:]))
		if len(fields) > 2 {
			if pgid, err := strconv.Atoi(fields[2]); err == nil {
				return pgid
			}
		}
	}
	t.Fatalf("no process of this host carries the job's marker %s", marker)
	return 0
}
