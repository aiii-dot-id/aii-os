//go:build windows

package firewall

import (
	"io/fs"
	"os"

	"golang.org/x/sys/windows"
)

func byHandle(h windows.Handle) (fileID, uint64, error) {
	var d windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(h, &d); err != nil {
		return fileID{}, 0, err
	}
	return fileID{uint64(d.VolumeSerialNumber), uint64(d.FileIndexHigh)<<32 | uint64(d.FileIndexLow)}, uint64(d.NumberOfLinks), nil
}

func namesOf(file os.FileInfo, by *os.File) (fileID, uint64, bool) {
	if file == nil || by == nil || !file.Mode().IsRegular() {
		return fileID{}, 0, false
	}
	conn, err := by.SyscallConn()
	if err != nil {
		return fileID{}, 0, false
	}
	var id fileID
	var names uint64
	var qerr error
	if err := conn.Control(func(fd uintptr) { id, names, qerr = byHandle(windows.Handle(fd)) }); err != nil || qerr != nil {
		return fileID{}, 0, false
	}
	return id, names, true
}

func openAttributes(path string, follow bool) (windows.Handle, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return windows.InvalidHandle, &fs.PathError{Op: "open", Path: path, Err: err}
	}
	flags := uint32(windows.FILE_FLAG_BACKUP_SEMANTICS)
	if !follow {
		flags |= windows.FILE_FLAG_OPEN_REPARSE_POINT
	}
	h, err := windows.CreateFile(p, windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, flags, 0)
	if err != nil {
		return windows.InvalidHandle, &fs.PathError{Op: "open", Path: path, Err: err}
	}
	return h, nil
}

func namesAt(path string) (os.FileInfo, fileID, uint64, error) {
	h, err := openAttributes(path, true)
	if err != nil {
		return nil, fileID{}, 0, err
	}
	f := os.NewFile(uintptr(h), path)
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, fileID{}, 0, err
	}
	id, names, err := byHandle(h)
	if err != nil {
		return nil, fileID{}, 0, &fs.PathError{Op: "GetFileInformationByHandle", Path: path, Err: err}
	}
	return info, id, names, nil
}

func entryID(path string, _ fs.DirEntry) (fileID, error) {
	h, err := openAttributes(path, false)
	if err != nil {
		return fileID{}, err
	}
	defer windows.CloseHandle(h)
	id, _, err := byHandle(h)
	if err != nil {
		return fileID{}, &fs.PathError{Op: "GetFileInformationByHandle", Path: path, Err: err}
	}
	return id, nil
}
