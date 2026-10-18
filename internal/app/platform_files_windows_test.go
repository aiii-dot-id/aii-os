//go:build windows

package app

import (
	"os"
	"syscall"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"golang.org/x/sys/windows"
)

// goneNetworkHome is what a read answers on a network share whose
// connection went: ERROR_NETNAME_DELETED.
const goneNetworkHome = syscall.Errno(windows.ERROR_NETNAME_DELETED)

func makeUnreadable(t *testing.T, path string) bool {
	t.Helper()
	denied := func(op, p string) error {
		return &os.PathError{Op: op, Path: p, Err: syscall.Errno(windows.ERROR_ACCESS_DENIED)}
	}
	prevKey, prevNamed := loadKeyPair, namedKey
	t.Cleanup(func() { loadKeyPair, namedKey = prevKey, prevNamed })
	loadKeyPair = func(p string) (*crypto.KeyPair, error) {
		if p == path {
			return nil, denied("open", p)
		}
		return prevKey(p)
	}
	namedKey = func(p string) ([]byte, string, error) {
		if p == path {
			return nil, "", denied("open", p)
		}
		return prevNamed(p)
	}
	return false
}
