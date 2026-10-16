//go:build !windows

package tools

import (
	"fmt"
	"os"
	"syscall"
)

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

func openToWrite(path string, perm os.FileMode, create bool) (*os.File, os.FileInfo, error) {
	extra := 0
	if create {
		extra = os.O_CREATE | os.O_EXCL
	}
	return openForReplace(path, perm, extra)
}

func rewriteSameFile(path string, data []byte, measured os.FileInfo, held func(string, os.FileInfo, *os.File) error) error {
	f, st, err := openForReplace(path, 0, 0)
	if err != nil {
		return err
	}
	if !os.SameFile(measured, st) {
		f.Close()
		return errNotTheFileRead(path)
	}
	if held != nil {
		if kept := held(path, st, f); kept != nil {
			f.Close()
			return kept
		}
	}
	return truncateWriteClose(f, data)
}

var errCrossDevice error = syscall.EXDEV

func noFollowError(path string, err error) error {
	if pe, ok := err.(*os.PathError); ok {
		switch pe.Err {
		case syscall.ELOOP:
			return fmt.Errorf("%s is a symlink — writes go to real files only (symlink targets can move between check and use)", path)
		case syscall.ENXIO:

			return fmt.Errorf("%s is not a regular file — write refused", path)
		}
	}
	return err
}

func openWriteReceipt(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
}
