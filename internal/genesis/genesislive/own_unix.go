//go:build !windows

package genesislive

import (
	"os"
	"strconv"
	"syscall"
)

func accountTag() string { return strconv.Itoa(os.Geteuid()) }

func privateDir(d string) bool {
	if err := os.MkdirAll(d, 0o700); err != nil {
		return false
	}
	st, err := os.Lstat(d)
	if err != nil || !st.IsDir() {
		return false
	}
	sys, ok := st.Sys().(*syscall.Stat_t)
	if !ok || sys.Uid != uint32(os.Geteuid()) || st.Mode().Perm()&0o022 != 0 {
		return false
	}
	if st.Mode().Perm()&0o077 != 0 {
		_ = os.Chmod(d, 0o700)
	}
	return true
}

func openOwn(path string) (*os.File, os.FileInfo, bool) {
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, nil, false
	}
	f := os.NewFile(uintptr(fd), path)
	st, err := f.Stat()
	if err == nil {
		sys, ok := st.Sys().(*syscall.Stat_t)
		if st.Mode().IsRegular() && ok && sys.Uid == uint32(os.Geteuid()) && st.Mode().Perm()&0o022 == 0 {
			return f, st, true
		}
	}
	_ = f.Close()
	return nil, nil, false
}
