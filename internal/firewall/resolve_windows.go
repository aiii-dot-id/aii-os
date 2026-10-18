//go:build windows

package firewall

import (
	"os"
	"strings"

	"golang.org/x/sys/windows"
)

func evalLinks(path string) (string, error) {
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
	buf := make([]uint16, windows.MAX_PATH)
	for {
		n, err := windows.GetFinalPathNameByHandle(h, &buf[0], uint32(len(buf)), volumeNameDOS)
		if err != nil {
			return "", &os.PathError{Op: "final path", Path: path, Err: err}
		}
		if int(n) < len(buf) {
			return dosPath(windows.UTF16ToString(buf[:n])), nil
		}
		buf = make([]uint16, n+1)
	}
}

const volumeNameDOS = 0

func dosPath(final string) string {
	switch {
	case strings.HasPrefix(final, `\\?\UNC\`):
		return `\\` + final[len(`\\?\UNC\`):]
	case strings.HasPrefix(final, `\\?\`):
		return final[len(`\\?\`):]
	}
	return final
}

func linkTarget(path string) (target string, linked bool, err error) {
	fi, err := os.Lstat(path)
	if err != nil || fi.Mode()&(os.ModeSymlink|os.ModeIrregular) == 0 {
		return "", false, nil
	}
	t, err := os.Readlink(path)
	if err != nil {
		return "", true, err
	}
	return strings.TrimPrefix(t, `\??\`), true, nil
}
