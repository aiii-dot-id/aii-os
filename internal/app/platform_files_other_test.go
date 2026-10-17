//go:build !windows

package app

import (
	"os"
	"syscall"
	"testing"
)

// goneNetworkHome is what a read answers on a network home whose server
// lost the handle: ESTALE.
const goneNetworkHome = syscall.ESTALE

// makeUnreadable leaves path unreadable to this process for the test's
// life, its mode here, and reports true: every reader of the file meets
// it. The superuser reads every file, so a test that asks for it skips as
// the superuser.
func makeUnreadable(t *testing.T, path string) bool {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("the superuser reads every file")
	}
	if err := os.Chmod(path, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(path, 0o600) })
	return true
}
