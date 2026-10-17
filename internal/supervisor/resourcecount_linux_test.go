//go:build linux

package supervisor

import (
	"os"
	"testing"
)

// openResourceCount is this process's open descriptors.
func openResourceCount(t *testing.T) int {
	t.Helper()
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Skipf("no /proc/self/fd: %v", err)
	}
	return len(entries)
}
