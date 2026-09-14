//go:build !((linux && !android) || (darwin && !ios) || windows)

package install

import (
	"fmt"
	"runtime"
)

// .
const Unit = "aii-os"

// .
// .
// .
// .
// .
// .
// .
// .
func Register(slot string) (started bool, notes []string, err error) {
	return false, []string{
		"service registration is not automated on " + runtime.GOOS + " yet — the slot is ready; start it with the command above",
	}, nil
}

// .
func Unregister(slot string) error { return nil }

// .
func StartCommand(slot, dir string) string {
	return "aii -dir " + dir
}

// .
func Stop(slot string) error {
	return fmt.Errorf("stopping a service is not automated on %s — stop the process by hand", runtime.GOOS)
}
