//go:build linux

package supervisor

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

import (
	"fmt"
	"syscall"
	"unsafe"
)

// .
// .
// .
// .
// .
// .
func applyAddressSpaceLimit(pid int, bytes uint64) (string, error) {
	limit := syscall.Rlimit{Cur: bytes, Max: bytes}
	// .
	// .
	// .
	_, _, errno := syscall.RawSyscall6(syscall.SYS_PRLIMIT64,
		uintptr(pid), uintptr(syscall.RLIMIT_AS),
		uintptr(unsafe.Pointer(&limit)), 0, 0, 0)
	if errno != 0 {
		return "", fmt.Errorf("RLIMIT_AS %d bytes could not be applied (prlimit: %v)", bytes, errno)
	}
	return fmt.Sprintf("RLIMIT_AS %d bytes applied", bytes), nil
}
