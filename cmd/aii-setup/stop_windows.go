//go:build windows

package main

import (
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// .
// .
// .
// .
// .
// .
// .
// .
func ownsPath(exePath, installDir string) bool {
	if exePath == "" || installDir == "" {
		return false
	}
	dir := strings.ToLower(filepath.Clean(installDir))
	p := strings.ToLower(filepath.Clean(exePath))
	if !strings.HasSuffix(dir, string(filepath.Separator)) {
		dir += string(filepath.Separator)
	}
	return strings.HasPrefix(p, dir)
}

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
func stopOurProcesses(installDir string) {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return
	}
	defer windows.CloseHandle(snap)

	self := uint32(os.Getpid())
	var e windows.ProcessEntry32
	e.Size = uint32(unsafe.Sizeof(e))
	if err := windows.Process32First(snap, &e); err != nil {
		return
	}
	for {
		pid := e.ProcessID
		if pid != 0 && pid != self {
			if path, ok := processPath(pid); ok && ownsPath(path, installDir) {
				if h, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, pid); err == nil {
					_ = windows.TerminateProcess(h, 0)
					windows.CloseHandle(h)
				}
			}
		}
		if err := windows.Process32Next(snap, &e); err != nil {
			return
		}
	}
}

// .
// .
func processPath(pid uint32) (string, bool) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return "", false
	}
	defer windows.CloseHandle(h)
	buf := make([]uint16, windows.MAX_LONG_PATH)
	n := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(h, 0, &buf[0], &n); err != nil {
		return "", false
	}
	return windows.UTF16ToString(buf[:n]), true
}
