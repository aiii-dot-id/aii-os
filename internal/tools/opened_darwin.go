//go:build darwin || ios

package tools

import (
	"os"
	"runtime"
	"unsafe"

	"golang.org/x/sys/unix"
)

func openedAt(by *os.File) (string, error) {
	raw, err := by.SyscallConn()
	if err != nil {
		return "", err
	}
	var buf [unix.PathMax]byte
	var askErr error
	if err := raw.Control(func(fd uintptr) {
		_, askErr = unix.FcntlInt(fd, unix.F_GETPATH, int(uintptr(unsafe.Pointer(&buf[0]))))
	}); err != nil {
		return "", err
	}
	runtime.KeepAlive(&buf)
	if askErr != nil {
		return "", askErr
	}
	return unix.ByteSliceToString(buf[:]), nil
}
