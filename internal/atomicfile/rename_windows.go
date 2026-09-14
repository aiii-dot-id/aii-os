//go:build windows

package atomicfile

import (
	"errors"
	"os"
	"time"

	"golang.org/x/sys/windows"
)

// .
// .
// .
// .
// .
// .
// .
// .
// .
const renameRetryBudget = 2 * time.Second

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
func Rename(from, to string) error {
	deadline := time.Now().Add(renameRetryBudget)
	delay := 500 * time.Microsecond
	for {
		err := os.Rename(from, to)
		if err == nil || !renameContended(err) || time.Now().After(deadline) {
			return err
		}
		time.Sleep(delay)
		if delay < 20*time.Millisecond {
			delay *= 2
		}
	}
}

func renameContended(err error) bool {
	return errors.Is(err, windows.ERROR_ACCESS_DENIED) ||
		errors.Is(err, windows.ERROR_SHARING_VIOLATION)
}
