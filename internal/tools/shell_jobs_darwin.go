//go:build darwin || ios

package tools

import "golang.org/x/sys/unix"

const szomb = 5

func processGroups() map[int]map[int]uint64 {
	procs, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		return nil
	}
	groups := make(map[int]map[int]uint64)
	for i := range procs {
		p := &procs[i]
		if p.Proc.P_stat == szomb {
			continue
		}
		pgid := int(p.Eproc.Pgid)
		if groups[pgid] == nil {
			groups[pgid] = make(map[int]uint64)
		}
		groups[pgid][int(p.Proc.P_pid)] = uint64(p.Proc.P_starttime.Sec)*1_000_000 + uint64(p.Proc.P_starttime.Usec)
	}
	return groups
}
