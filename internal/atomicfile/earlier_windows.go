//go:build windows

package atomicfile

import (
	"errors"
	"fmt"
	"io/fs"
	"os"

	"golang.org/x/sys/windows"
)

func openEarlier(oldPath, newPath string) (*earlier, error) {
	f, err := openToEmpty(newPath)
	if err != nil {
		return nil, fmt.Errorf("%s was not replaced: %w", newPath, err)
	}
	if f == nil {
		return nil, nil
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("%s could not be asked whether it has another name, so it was not replaced: %w", newPath, err)
	}
	if src, err := os.Stat(oldPath); err == nil && os.SameFile(src, st) {
		f.Close()
		return nil, nil
	}
	return &earlier{f}, nil
}

func namesOf(f *os.File) (uint64, error) {
	conn, err := f.SyscallConn()
	if err != nil {
		return 0, err
	}
	var d windows.ByHandleFileInformation
	var qerr error
	if err := conn.Control(func(fd uintptr) { qerr = windows.GetFileInformationByHandle(windows.Handle(fd), &d) }); err != nil {
		return 0, err
	}
	if qerr != nil {
		return 0, qerr
	}
	return uint64(d.NumberOfLinks), nil
}

func openToEmpty(path string) (*os.File, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	share := uint32(windows.FILE_SHARE_READ | windows.FILE_SHARE_WRITE | windows.FILE_SHARE_DELETE)
	flags := uint32(windows.FILE_FLAG_BACKUP_SEMANTICS | windows.FILE_FLAG_OPEN_REPARSE_POINT)
	h, err := windows.CreateFile(name, windows.FILE_READ_ATTRIBUTES, share, nil, windows.OPEN_EXISTING, flags, 0)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("it could not be asked whether it has another name: %w", err)
	}
	var d windows.ByHandleFileInformation
	err = windows.GetFileInformationByHandle(h, &d)
	windows.CloseHandle(h)
	if err != nil {
		return nil, fmt.Errorf("it could not be asked whether it has another name: %w", err)
	}
	if d.FileAttributes&(windows.FILE_ATTRIBUTE_DIRECTORY|windows.FILE_ATTRIBUTE_REPARSE_POINT) != 0 || d.NumberOfLinks < 2 {
		return nil, nil
	}
	if d.FileAttributes&windows.FILE_ATTRIBUTE_READONLY != 0 {
		if err := windows.SetFileAttributes(name, d.FileAttributes&^windows.FILE_ATTRIBUTE_READONLY); err != nil {
			return nil, fmt.Errorf("it has another name, and is read-only, and could not be made writable to be emptied (%w); fsutil hardlink list %s lists its other names", err, path)
		}
	}
	err = retryContended(func() error {
		h, err = windows.CreateFile(name, windows.GENERIC_WRITE, share, nil, windows.OPEN_EXISTING, flags, 0)
		return err
	}, renameContended)
	if err != nil {
		return nil, fmt.Errorf("it has another name, and could not be opened to be emptied (%w); fsutil hardlink list %s lists its other names", err, path)
	}
	return os.NewFile(uintptr(h), path), nil
}

func openToRemove(path string) (*os.File, error) {
	f, err := openToEmpty(path)
	if err != nil || f != nil {
		return f, err
	}
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, nil
	}
	open := func() (windows.Handle, error) {
		return windows.CreateFile(name, windows.GENERIC_WRITE,
			windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
			nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	}
	h, err := open()
	if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		if a, aerr := windows.GetFileAttributes(name); aerr == nil && a&windows.FILE_ATTRIBUTE_READONLY != 0 && a&(windows.FILE_ATTRIBUTE_DIRECTORY|windows.FILE_ATTRIBUTE_REPARSE_POINT) == 0 {
			if windows.SetFileAttributes(name, a&^windows.FILE_ATTRIBUTE_READONLY) == nil {
				h, err = open()
			}
		}
	}
	if err != nil {
		return nil, nil
	}
	var d windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(h, &d); err != nil || d.FileAttributes&(windows.FILE_ATTRIBUTE_DIRECTORY|windows.FILE_ATTRIBUTE_REPARSE_POINT) != 0 {
		windows.CloseHandle(h)
		return nil, nil
	}
	return os.NewFile(uintptr(h), path), nil
}
