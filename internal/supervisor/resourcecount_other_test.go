//go:build !linux && !windows

package supervisor

import "testing"

// .
// .
func openResourceCount(t *testing.T) int {
	t.Helper()
	t.Skip("no open-resource count on this platform")
	return 0
}
