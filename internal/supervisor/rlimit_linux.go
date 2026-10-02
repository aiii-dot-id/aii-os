//go:build linux || android

package supervisor

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
	"unsafe"
)

var errAddressSpaceExceeded = errors.New("child already exceeds its address-space limit")

func applyAddressSpaceLimit(pid int, bytes uint64) (string, error) {
	limit := syscall.Rlimit{Cur: bytes, Max: bytes}

	_, _, errno := syscall.RawSyscall6(syscall.SYS_PRLIMIT64,
		uintptr(pid), uintptr(syscall.RLIMIT_AS),
		uintptr(unsafe.Pointer(&limit)), 0, 0, 0)
	if errno != 0 {
		return "", fmt.Errorf("RLIMIT_AS %d bytes could not be applied: %w", bytes, errno)
	}

	statm, err := os.ReadFile(fmt.Sprintf("/proc/%d/statm", pid))
	if err != nil {
		return "", fmt.Errorf("verify RLIMIT_AS %d bytes: %w", bytes, err)
	}
	fields := strings.Fields(string(statm))
	if len(fields) == 0 {
		return "", fmt.Errorf("verify RLIMIT_AS %d bytes: missing mapped-page count", bytes)
	}
	pages, err := strconv.ParseUint(fields[0], 10, 64)
	if err != nil {
		return "", fmt.Errorf("verify RLIMIT_AS %d bytes: %w", bytes, err)
	}
	pageSize := uint64(os.Getpagesize())
	if pages > bytes/pageSize {
		return "", fmt.Errorf("%w: %d mapped pages of %d bytes, configured ceiling %d bytes; increase memory_max_bytes for this child", errAddressSpaceExceeded, pages, pageSize, bytes)
	}
	return fmt.Sprintf("RLIMIT_AS %d bytes applied", bytes), nil
}
