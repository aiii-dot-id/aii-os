//go:build windows

package updates

import (
	"os"
	"strings"

	"golang.org/x/sys/windows"
)

func finalPathOf(path string) (string, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return "", err
	}
	h, err := windows.CreateFile(name, windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return "", &os.PathError{Op: "open", Path: path, Err: err}
	}
	defer windows.CloseHandle(h)
	final, err := finalPathByHandle(h, volumeNameDOS)
	if err != nil {
		if final, err = finalPathByHandle(h, volumeNameGUID); err != nil {
			return "", &os.PathError{Op: "final path", Path: path, Err: err}
		}
		return final, nil
	}
	switch {
	case strings.HasPrefix(final, `\\?\UNC\`):
		return `\\` + final[len(`\\?\UNC\`):], nil
	case strings.HasPrefix(final, `\\?\`):
		return final[len(`\\?\`):], nil
	}
	return final, nil
}

const (
	volumeNameDOS  = 0x0
	volumeNameGUID = 0x1
)

func finalPathByHandle(h windows.Handle, flags uint32) (string, error) {
	buf := make([]uint16, windows.MAX_PATH)
	for {
		n, err := windows.GetFinalPathNameByHandle(h, &buf[0], uint32(len(buf)), flags)
		if err != nil {
			return "", err
		}
		if int(n) < len(buf) {
			return windows.UTF16ToString(buf[:n]), nil
		}
		buf = make([]uint16, n+1)
	}
}
