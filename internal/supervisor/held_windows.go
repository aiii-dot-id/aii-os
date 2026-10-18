//go:build windows

package supervisor

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

func treeHeld(root int) (int64, bool) {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return 0, false
	}
	defer windows.CloseHandle(snap)
	parent := map[int]int{}
	var e windows.ProcessEntry32
	e.Size = uint32(unsafe.Sizeof(e))
	for err = windows.Process32First(snap, &e); err == nil; err = windows.Process32Next(snap, &e) {
		parent[int(e.ProcessID)] = int(e.ParentProcessID)
	}
	tree := descendants(root, parent)
	if len(tree) == 0 {
		return 0, false
	}
	var total int64
	for _, pid := range tree {
		held, ok := processHeld(uint32(pid))
		if !ok && pid == root {
			return 0, false
		}
		total += held
	}
	return total, true
}

type processMemoryCountersEx struct {
	CB                         uint32
	PageFaultCount             uint32
	PeakWorkingSetSize         uintptr
	WorkingSetSize             uintptr
	QuotaPeakPagedPoolUsage    uintptr
	QuotaPagedPoolUsage        uintptr
	QuotaPeakNonPagedPoolUsage uintptr
	QuotaNonPagedPoolUsage     uintptr
	PagefileUsage              uintptr
	PeakPagefileUsage          uintptr
	PrivateUsage               uintptr
}

var procGetProcessMemoryInfo = windows.NewLazySystemDLL("psapi.dll").NewProc("GetProcessMemoryInfo")

func processHeld(pid uint32) (int64, bool) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return 0, false
	}
	defer windows.CloseHandle(h)
	var c processMemoryCountersEx
	c.CB = uint32(unsafe.Sizeof(c))
	if ok, _, _ := procGetProcessMemoryInfo.Call(uintptr(h), uintptr(unsafe.Pointer(&c)), uintptr(c.CB)); ok == 0 {
		return 0, false
	}
	return int64(min(c.WorkingSetSize, c.PrivateUsage)), true
}
