//go:build linux || android

package atomicfile

import (
	"fmt"
	"os"
	"strconv"
	"syscall"

	"golang.org/x/sys/unix"
)

var procSelfFD = "/proc/self/fd/"

func ownerWritable(path string, info os.FileInfo) error {
	mode := info.Mode().Perm() | 0o200
	fd, err := unix.Open(path, unix.O_PATH|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return &os.PathError{Op: "open", Path: path, Err: err}
	}
	defer unix.Close(fd)
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		return &os.PathError{Op: "fstat", Path: path, Err: err}
	}
	was, ok := info.Sys().(*syscall.Stat_t)
	if st.Mode&unix.S_IFMT != unix.S_IFREG || !ok || uint64(st.Dev) != uint64(was.Dev) || uint64(st.Ino) != uint64(was.Ino) {
		return fmt.Errorf("%s changed while it was made writable", path)
	}
	if err := os.Chmod(procSelfFD+strconv.Itoa(fd), mode); err != nil {
		return fmt.Errorf("%s could not be made writable through %s: %w", path, procSelfFD, err)
	}
	return nil
}
