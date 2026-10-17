//go:build (linux && !android) || (darwin && !ios) || windows

package install

import (
	"path/filepath"
	"testing"
)

// ownHome points HOME (and USERPROFILE) at a home of the test's own and
// has it taken for the account's home as the OS records it, so the
// registrations a test makes there are its own account's (OwnAccount).
func ownHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	if r, err := filepath.EvalSymlinks(home); err == nil {
		home = r
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("SUDO_USER", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Cleanup(AccountHomeForTest(home))
	return home
}
