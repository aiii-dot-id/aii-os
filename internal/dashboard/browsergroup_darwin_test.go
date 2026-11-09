//go:build darwin && !ios

package dashboard

import "golang.org/x/sys/unix"

// processGroupMembers lists the running processes of a process group. One that
// has ended and waits to be reaped holds nothing, and is not listed.
func processGroupMembers(pgid int) ([]int, error) {
	kinfo, err := unix.SysctlKinfoProcSlice("kern.proc.pgrp", pgid)
	if err != nil {
		return nil, err
	}
	var running []int
	for _, k := range kinfo {
		if k.Proc.P_stat != szomb {
			running = append(running, int(k.Proc.P_pid))
		}
	}
	return running, nil
}

// szomb is a process that has ended and waits to be reaped (sys/proc.h).
const szomb = 5
