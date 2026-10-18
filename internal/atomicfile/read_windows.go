//go:build windows

package atomicfile

import (
	"io"
	"os"

	"golang.org/x/sys/windows"
)

func Open(path string) (*os.File, error) {
	var f *os.File
	err := retryContended(func() error {
		name, err := windows.UTF16PtrFromString(path)
		if err != nil {
			return &os.PathError{Op: "open", Path: path, Err: err}
		}
		h, err := windows.CreateFile(name, windows.GENERIC_READ,
			windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
			nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
		if err != nil {
			return &os.PathError{Op: "open", Path: path, Err: err}
		}
		f = os.NewFile(uintptr(h), path)
		return nil
	}, renameContended)
	if err != nil {
		return nil, err
	}
	return f, nil
}

func ReadFile(path string) ([]byte, error) {
	f, err := Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(f)
}
