//go:build windows

package atomicfile

import (
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

func posixRename(from, to string, flush func(windows.Handle) error) (taken, renamed bool, err error) {

	target, err := filepath.Abs(to)
	if err != nil {
		return false, false, err
	}
	if len(target) >= 248 && !strings.HasPrefix(target, `\\`) {
		target = `\\?\` + target
	}
	name, err := windows.UTF16FromString(target)
	if err != nil {
		return false, false, err
	}
	source, err := windows.UTF16PtrFromString(from)
	if err != nil {
		return false, false, err
	}

	access := uint32(windows.DELETE | windows.SYNCHRONIZE)
	attrs := uint32(windows.FILE_ATTRIBUTE_NORMAL | windows.FILE_FLAG_BACKUP_SEMANTICS | windows.FILE_FLAG_OPEN_REPARSE_POINT)
	if flush != nil {
		access |= windows.GENERIC_WRITE
		attrs |= windows.FILE_FLAG_WRITE_THROUGH
	}
	h, err := windows.CreateFile(source, access,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, attrs, 0)
	if err != nil {
		return false, false, err
	}
	defer windows.CloseHandle(h)

	const nameAt, structSize = 20, 24
	nameBytes := (len(name) - 1) * 2
	size := structSize + nameBytes
	backing := make([]uint64, (size+7)/8)
	buf := unsafe.Slice((*byte)(unsafe.Pointer(&backing[0])), len(backing)*8)
	*(*uint32)(unsafe.Pointer(&buf[0])) = windows.FILE_RENAME_REPLACE_IF_EXISTS | windows.FILE_RENAME_POSIX_SEMANTICS
	*(*uint32)(unsafe.Pointer(&buf[16])) = uint32(nameBytes)
	copy(unsafe.Slice((*uint16)(unsafe.Pointer(&buf[nameAt])), len(name)), name)
	if err := windows.SetFileInformationByHandle(h, windows.FileRenameInfoEx, &buf[0], uint32(size)); err != nil {
		return renameContended(err), false, err
	}
	if flush != nil {
		if err := flush(h); err != nil {
			return true, true, err
		}
	}
	return true, true, nil
}
