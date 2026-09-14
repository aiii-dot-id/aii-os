//go:build windows

package supervisor

import "fmt"

// .
// .
// .
// .
// .
// .
// .
// .
func applyAddressSpaceLimit(pid int, bytes uint64) (string, error) {
	return fmt.Sprintf("memory envelope %d bytes rides the containment job object (one job — nested UI+memory jobs are refused by Windows)", bytes), nil
}
