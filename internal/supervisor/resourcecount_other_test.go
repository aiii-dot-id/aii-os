//go:build !linux && !windows

package supervisor

import "testing"

// openResourceCount has no portable counterpart here; the leak proof runs
// where the resource can be counted, and says so where it cannot.
func openResourceCount(t *testing.T) int {
	t.Helper()
	t.Skip("no open-resource count on this platform")
	return 0
}
