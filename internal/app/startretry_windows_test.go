//go:build windows

package app

import (
	"os"
	"syscall"
	"testing"

	"golang.org/x/sys/windows"
)

// A file another program holds on Windows (a sharing or a lock violation)
// may pass with time: a scanner or a backup tool lets it go. So may a home
// on a network share whose connection went or did not answer in time,
// Windows' words for what ESTALE and EIO say elsewhere.
func TestAFileAnotherProgramHoldsMayPassWithTime(t *testing.T) {
	for _, errno := range []syscall.Errno{
		syscall.Errno(windows.ERROR_SHARING_VIOLATION), syscall.Errno(windows.ERROR_LOCK_VIOLATION),
		syscall.Errno(windows.ERROR_NETNAME_DELETED), syscall.Errno(windows.ERROR_UNEXP_NET_ERR),
		syscall.Errno(windows.ERROR_BAD_NETPATH), syscall.Errno(windows.ERROR_SEM_TIMEOUT),
	} {
		if !passesWithTime(stopped("open verified ledger", &os.PathError{Op: "open", Path: "ledger.jsonl", Err: errno})) {
			t.Errorf("WINDOWS' %v IS HELD AT THE PAGE FOR A PERSON, not tried again", errno)
		}
	}
}
