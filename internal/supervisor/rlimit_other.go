//go:build !linux && !windows

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
// .
// .

import "fmt"

// .
// .
// .
// .
// .
func applyAddressSpaceLimit(pid int, bytes uint64) (string, error) {
	return fmt.Sprintf("RLIMIT_AS %d bytes requested but NOT ENFORCED on this platform (no cross-process rlimit mechanism)", bytes), nil
}
