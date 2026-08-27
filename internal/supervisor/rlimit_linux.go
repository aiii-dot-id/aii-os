//go:build linux

package supervisor

// The native-T3 address-space envelope, Linux mechanism: prlimit(2) on
// the child pid, applied immediately after spawn. Linux is the one
// platform with a from-outside per-process rlimit call; the window
// between exec and the limit landing is microseconds during which the
// child has not yet been sent a single frame, so no plugin work runs
// unenveloped.
//
// This is a platform-owned file under the five-platform law: the
// mechanism lives here, the honest no-op record for platforms without
// one lives in rlimit_other.go, and pure-Go core code never branches
// on GOOS.

import (
	"fmt"
	"syscall"
	"unsafe"
)

// applyAddressSpaceLimit sets RLIMIT_AS (cur=max=bytes) on pid.
//
// Returns (telemetry, err). A non-nil err means THE MECHANISM EXISTS
// AND REFUSED — the caller must not run the child, because an operator
// who configured an envelope did not get one. "" telemetry with a nil
// err is silent success.
func applyAddressSpaceLimit(pid int, bytes uint64) (string, error) {
	limit := syscall.Rlimit{Cur: bytes, Max: bytes}
	// prlimit64(pid, resource, new_limit, old_limit) — raw because the
	// syscall package exposes the number but not a wrapper, and the
	// repo's tidy-proof dependency pins rule out x/sys for one call.
	_, _, errno := syscall.RawSyscall6(syscall.SYS_PRLIMIT64,
		uintptr(pid), uintptr(syscall.RLIMIT_AS),
		uintptr(unsafe.Pointer(&limit)), 0, 0, 0)
	if errno != 0 {
		return "", fmt.Errorf("RLIMIT_AS %d bytes could not be applied (prlimit: %v)", bytes, errno)
	}
	return fmt.Sprintf("RLIMIT_AS %d bytes applied", bytes), nil
}
