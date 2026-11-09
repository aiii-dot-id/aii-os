//go:build linux && !android

package dashboard

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// processGroupMembers lists the running processes of a process group. One that
// has ended and waits to be reaped holds nothing, and is not listed.
func processGroupMembers(pgid int) ([]int, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	var running []int
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		raw, err := os.ReadFile("/proc/" + e.Name() + "/stat") // a process may end between the two
		if err != nil {
			continue
		}
		// pid (comm) state ppid pgrp ..., and comm may hold anything.
		var state string
		var ppid, pgrp int
		if _, err := fmt.Sscan(string(raw[strings.LastIndexByte(string(raw), ')')+1:]), &state, &ppid, &pgrp); err != nil {
			return nil, fmt.Errorf("/proc/%d/stat: %w", pid, err)
		}
		if pgrp == pgid && state != "Z" {
			running = append(running, pid)
		}
	}
	return running, nil
}
