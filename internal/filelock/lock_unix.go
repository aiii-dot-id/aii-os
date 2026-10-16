//go:build linux || darwin || android || ios

package filelock

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

func Lock(f *os.File) error {
	return flock(f, unix.LOCK_EX)
}

func RLock(f *os.File) error {
	return flock(f, unix.LOCK_SH)
}

func flock(f *os.File, how int) error {
	err := unix.Flock(int(f.Fd()), how|unix.LOCK_NB)
	if errors.Is(err, unix.EWOULDBLOCK) {
		return fmt.Errorf("%w: %w", ErrHeld, err)
	}
	return err
}
