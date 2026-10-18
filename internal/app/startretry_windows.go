//go:build windows

package app

import (
	"syscall"

	"golang.org/x/sys/windows"
)

func passesWithTimeHere(errno syscall.Errno) bool {
	switch errno {
	case syscall.Errno(windows.ERROR_SHARING_VIOLATION), syscall.Errno(windows.ERROR_LOCK_VIOLATION),
		syscall.Errno(windows.ERROR_NETNAME_DELETED), syscall.Errno(windows.ERROR_UNEXP_NET_ERR),
		syscall.Errno(windows.ERROR_BAD_NETPATH), syscall.Errno(windows.ERROR_SEM_TIMEOUT):
		return true
	}
	return false
}

const defaultEndedStartsRestart = false
