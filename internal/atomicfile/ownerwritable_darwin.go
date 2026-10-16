//go:build darwin || ios

package atomicfile

import (
	"encoding/binary"
	"fmt"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

func ownerWritable(path string, info os.FileInfo) error {
	attrs := modeWithoutACLData(info.Mode().Perm() | 0o200)
	if f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0); err == nil {
		defer f.Close()
		st, err := f.Stat()
		if err != nil {
			return err
		}
		if !st.Mode().IsRegular() || !os.SameFile(st, info) {
			return fmt.Errorf("%s changed while it was made writable", path)
		}
		if attrWindow != nil {
			attrWindow("call", path)
		}
		conn, err := f.SyscallConn()
		if err != nil {
			return err
		}
		var serr error
		if err := conn.Control(func(fd uintptr) { serr = fsetattrlist(fd, &modeWithoutACL, attrs, 0) }); err != nil {
			return err
		}
		if serr != nil {
			return &os.PathError{Op: "fsetattrlist", Path: path, Err: serr}
		}
		return nil
	}
	if attrWindow != nil {
		attrWindow("judged", path)
	}
	if err := stillThere(path, info); err != nil {
		return err
	}
	if attrWindow != nil {
		attrWindow("call", path)
	}
	if err := unix.Setattrlist(path, &modeWithoutACL, attrs, unix.FSOPT_NOFOLLOW); err != nil {
		return &os.PathError{Op: "setattrlist", Path: path, Err: err}
	}
	return stillThere(path, info)
}

var attrWindow func(stage, path string)

func stillThere(path string, info os.FileInfo) error {
	now, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !now.Mode().IsRegular() || !os.SameFile(now, info) {
		return fmt.Errorf("%s changed while it was made writable", path)
	}
	return nil
}

var modeWithoutACL = unix.Attrlist{Bitmapcount: unix.ATTR_BIT_MAP_COUNT, Commonattr: unix.ATTR_CMN_ACCESSMASK | unix.ATTR_CMN_EXTENDED_SECURITY}

const (
	kauthFilesecMagic = 0x012cc16d
	kauthFilesecNoACL = 0xffffffff
	kauthFilesecSize  = 4 + 16 + 16 + 4 + 4
)

func modeWithoutACLData(mode os.FileMode) []byte {
	b := make([]byte, 4+8+kauthFilesecSize)
	binary.NativeEndian.PutUint32(b[0:], uint32(mode))
	binary.NativeEndian.PutUint32(b[4:], 8)
	binary.NativeEndian.PutUint32(b[8:], kauthFilesecSize)
	binary.NativeEndian.PutUint32(b[12:], kauthFilesecMagic)
	binary.NativeEndian.PutUint32(b[12+36:], kauthFilesecNoACL)
	return b
}
