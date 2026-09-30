//go:build linux || darwin || android || ios

package ledger

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

func lockLedgerFile(file *os.File) error {
	err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if errors.Is(err, unix.EWOULDBLOCK) {
		return fmt.Errorf("%w: %w", ErrLedgerInUse, err)
	}
	return err
}

func openLedgerForRewrap(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDWR, 0)
}

func createLedgerForRewrap(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
}
