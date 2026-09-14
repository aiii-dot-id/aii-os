//go:build windows

package supervisor

import (
	"syscall"
	"testing"
	"unsafe"
)

var procGetProcessHandleCount = syscall.NewLazyDLL("kernel32.dll").NewProc("GetProcessHandleCount")

// .
// .
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
