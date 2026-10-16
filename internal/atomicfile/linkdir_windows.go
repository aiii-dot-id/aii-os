//go:build windows

package atomicfile

import (
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

func LinkDir(target, alias string) (err error) {
	target, err = filepath.Abs(target)
	if err != nil {
		return err
	}
	if strings.HasPrefix(target, `\\?\UNC\`) {
		target = `\\` + strings.TrimPrefix(target, `\\?\UNC\`)
	} else {
		target = strings.TrimPrefix(target, `\\?\`)
	}
	substitute := `\??\` + target
	if strings.HasPrefix(target, `\\`) {
		substitute = `\??\UNC\` + strings.TrimPrefix(target, `\\`)
	}
	sub, err := windows.UTF16FromString(substitute)
	if err != nil {
		return err
	}
	printName, err := windows.UTF16FromString(target)
	if err != nil {
		return err
	}
	buf := make([]byte, 16+2*(len(sub)+len(printName)))
	if len(buf) > windows.MAXIMUM_REPARSE_DATA_BUFFER_SIZE {
		return &os.PathError{Op: "create storage junction", Path: alias, Err: windows.ERROR_FILENAME_EXCED_RANGE}
	}
	binary.LittleEndian.PutUint32(buf[0:4], windows.IO_REPARSE_TAG_MOUNT_POINT)
	binary.LittleEndian.PutUint16(buf[4:6], uint16(len(buf)-8))
	binary.LittleEndian.PutUint16(buf[10:12], uint16(2*(len(sub)-1)))
	binary.LittleEndian.PutUint16(buf[12:14], uint16(2*len(sub)))
	binary.LittleEndian.PutUint16(buf[14:16], uint16(2*(len(printName)-1)))
	for i, c := range append(sub, printName...) {
		binary.LittleEndian.PutUint16(buf[16+2*i:], c)
	}
	if _, err := os.Lstat(alias); err == nil {
		return &os.PathError{Op: "link directory", Path: alias, Err: os.ErrExist}
	} else if !os.IsNotExist(err) {
		return err
	}
	staged, err := os.MkdirTemp(filepath.Dir(alias), ".dir-link-")
	if err != nil {
		return err
	}
	defer func() {
		if staged != "" {
			err = errors.Join(err, os.Remove(staged))
		}
	}()
	name, err := windows.UTF16PtrFromString(staged)
	if err != nil {
		return err
	}
	h, err := windows.CreateFile(name, windows.GENERIC_WRITE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return err
	}
	var returned uint32
	err = windows.DeviceIoControl(h, windows.FSCTL_SET_REPARSE_POINT, &buf[0], uint32(len(buf)), nil, 0, &returned, nil)
	err = errors.Join(err, windows.CloseHandle(h))
	if err != nil {
		return err
	}
	if err = Rename(staged, alias); err != nil {
		return err
	}
	staged = ""
	return err
}
