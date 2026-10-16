//go:build darwin && !ios

package machine

import (
	"runtime"
	"syscall"
	"unsafe"
)

//go:cgo_import_dynamic libc_launch_activate_socket launch_activate_socket "/usr/lib/libSystem.B.dylib"
//go:cgo_import_dynamic libc_free free "/usr/lib/libSystem.B.dylib"

var (
	libc_launch_activate_socket_trampoline_addr uintptr
	libc_free_trampoline_addr                   uintptr
)

//go:linkname syscall_syscall syscall.syscall
func syscall_syscall(fn, a1, a2, a3 uintptr) (r1, r2 uintptr, err syscall.Errno)

func launchActivateSocket(name string) ([]int, error) {
	cname := append([]byte(name), 0)
	var fds *int32
	var cnt uintptr
	r, _, _ := syscall_syscall(libc_launch_activate_socket_trampoline_addr,
		uintptr(unsafe.Pointer(&cname[0])), uintptr(unsafe.Pointer(&fds)), uintptr(unsafe.Pointer(&cnt)))
	runtime.KeepAlive(cname)
	if r != 0 {
		return nil, syscall.Errno(r)
	}
	if fds == nil {
		return nil, nil
	}
	out := make([]int, 0, cnt)
	for _, fd := range unsafe.Slice(fds, cnt) {
		out = append(out, int(fd))
	}

	syscall_syscall(libc_free_trampoline_addr, uintptr(unsafe.Pointer(fds)), 0, 0)
	return out, nil
}
