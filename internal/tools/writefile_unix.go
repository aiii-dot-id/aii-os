//go:build !windows

package tools

import (
	"fmt"
	"os"
	"syscall"
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
func openForReplace(path string, perm os.FileMode, extra int) (*os.File, os.FileInfo, error) {
	f, err := os.OpenFile(path, os.O_WRONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|extra, perm)
	if err != nil {
		return nil, nil, noFollowError(path, err)
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, nil, err
	}
	if !st.Mode().IsRegular() {
		f.Close()
		return nil, nil, fmt.Errorf("%s is not a regular file — write refused", path)
	}
	return f, st, nil
}

// .
// .
// .
// .
// .
func writeFileNoFollowSame(path string, data []byte, perm os.FileMode, measured os.FileInfo) (bool, error) {
	if measured == nil {
		// .
		f, _, err := openForReplace(path, perm, os.O_CREATE|os.O_EXCL)
		if err == nil {
			return true, truncateWriteClose(f, data)
		}
		if !os.IsExist(err) {
			return false, err
		}
	}
	f, st, err := openForReplace(path, perm, os.O_CREATE)
	if err != nil {
		return false, err
	}
	same := measured != nil && os.SameFile(measured, st)
	return same, truncateWriteClose(f, data)
}

// .
// .
// .
// .
// .
func rewriteSameFile(path string, data []byte, measured os.FileInfo) error {
	f, st, err := openForReplace(path, 0, 0)
	if err != nil {
		return err
	}
	if !os.SameFile(measured, st) {
		f.Close()
		return errNotTheFileRead(path)
	}
	return truncateWriteClose(f, data)
}

func truncateWriteClose(f *os.File, data []byte) error {
	if err := f.Truncate(0); err != nil {
		f.Close()
		return err
	}
	_, werr := f.Write(data)
	cerr := f.Close()
	if werr != nil {
		return werr
	}
	return cerr
}

func noFollowError(path string, err error) error {
	if pe, ok := err.(*os.PathError); ok {
		switch pe.Err {
		case syscall.ELOOP:
			return fmt.Errorf("%s is a symlink — writes go to real files only (symlink targets can move between check and use)", path)
		case syscall.ENXIO:
			// .
			// .
			return fmt.Errorf("%s is not a regular file — write refused", path)
		}
	}
	return err
}

// .
// .
// .
func openWriteReceipt(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
}
