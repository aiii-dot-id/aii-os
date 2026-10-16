//go:build darwin || ios

package atomicfile

import (
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

func fsetattrlist(fd uintptr, attrlist *unix.Attrlist, attrBuf []byte, options uint32) error {
	_, _, e := syscall_syscall6(libc_fsetattrlist_trampoline_addr, fd, uintptr(unsafe.Pointer(attrlist)), uintptr(unsafe.Pointer(&attrBuf[0])), uintptr(len(attrBuf)), uintptr(options), 0)
	runtime.KeepAlive(attrlist)
	runtime.KeepAlive(attrBuf)
	if e != 0 {
		return e
	}
	return nil
}

//go:linkname syscall_syscall6 syscall.syscall6
func syscall_syscall6(fn, a1, a2, a3, a4, a5, a6 uintptr) (r1, r2 uintptr, err syscall.Errno)

var libc_fsetattrlist_trampoline_addr uintptr

//go:cgo_import_dynamic libc_fsetattrlist fsetattrlist "/usr/lib/libSystem.B.dylib"
