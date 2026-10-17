//go:build windows

package supervisor

import (
	"syscall"
	"testing"
	"unsafe"
)

var procGetProcessHandleCount = syscall.NewLazyDLL("kernel32.dll").NewProc("GetProcessHandleCount")

// openResourceCount is this process's open HANDLES — the resource a
// Windows audio pair can leak, as descriptors are on Unix.
func openResourceCount(t *testing.T) int {
	t.Helper()
	self, err := syscall.GetCurrentProcess()
	if err != nil {
		t.Skipf("current process: %v", err)
	}
	var n uint32
	r, _, callErr := procGetProcessHandleCount.Call(uintptr(self), uintptr(unsafe.Pointer(&n)))
	if r == 0 {
		t.Skipf("GetProcessHandleCount: %v", callErr)
	}
	return int(n)
}
