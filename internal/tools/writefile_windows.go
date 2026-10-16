//go:build windows

package tools

import (
	"fmt"
	"os"
	"syscall"
)

func openToWrite(path string, perm os.FileMode, create bool) (*os.File, os.FileInfo, error) {
	flags := os.O_WRONLY
	if create {
		flags |= os.O_CREATE | os.O_EXCL
	}
	f, err := os.OpenFile(path, flags, perm)
	if err != nil {
		return nil, nil, err
	}
	st, err := f.Stat()
	if err == nil && !st.Mode().IsRegular() {
		err = fmt.Errorf("%s is not a regular file — write refused", path)
	}
	if err != nil {
		f.Close()
		return nil, nil, err
	}
	return f, st, nil
}

func rewriteSameFile(path string, data []byte, measured os.FileInfo, held func(string, os.FileInfo, *os.File) error) error {
	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	if !st.Mode().IsRegular() || !os.SameFile(measured, st) {
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

var errCrossDevice error = syscall.Errno(17)

func openWriteReceipt(path string) (*os.File, error) {
	st, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() {
		return nil, errReceiptNotRegular
	}
	f, _, err := openRegular(path)
	return f, err
}
