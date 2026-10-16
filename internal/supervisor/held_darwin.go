//go:build darwin && !ios

package supervisor

import (
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

func treeHeld(root int) (int64, bool) {
	procs, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		return 0, false
	}
	parent := make(map[int]int, len(procs))
	for i := range procs {
		parent[int(procs[i].Proc.P_pid)] = int(procs[i].Eproc.Ppid)
	}
	tree := descendants(root, parent)
	if len(tree) == 0 {
		return 0, false
	}
	var total int64
	for _, pid := range tree {
		held, ok := footprint(pid)
		if !ok && pid == root {
			return 0, false
		}
		total += held
	}
	return total, true
}

type rusageInfoV2 struct {
	UUID           [16]byte
	UserTime       uint64
	SystemTime     uint64
	PkgIdleWkups   uint64
	InterruptWkups uint64
	Pageins        uint64
	WiredSize      uint64
	ResidentSize   uint64
	PhysFootprint  uint64
	Rest           [10]uint64
}

const (
	sysProcInfo           = 336
	procInfoCallPidRusage = 9
	rusageInfoV2Flavor    = 2
)

func footprint(pid int) (int64, bool) {
	var ri rusageInfoV2
	if _, _, errno := syscall.Syscall6(sysProcInfo, procInfoCallPidRusage, uintptr(pid), rusageInfoV2Flavor, 0, uintptr(unsafe.Pointer(&ri)), 0); errno != 0 {
		return 0, false
	}
	return int64(ri.PhysFootprint), true
}
