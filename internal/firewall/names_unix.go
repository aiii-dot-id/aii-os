//go:build !windows

package firewall

import (
	"io/fs"
	"os"
	"syscall"
)

func idOf(info os.FileInfo) (fileID, uint64, bool) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fileID{}, 0, false
	}
	return fileID{uint64(st.Dev), uint64(st.Ino)}, uint64(st.Nlink), true
}

func namesOf(file os.FileInfo, by *os.File) (fileID, uint64, bool) {
	if file == nil || !file.Mode().IsRegular() {
		return fileID{}, 0, false
	}
	return idOf(file)
}

func namesAt(path string) (os.FileInfo, fileID, uint64, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fileID{}, 0, err
	}
	id, names, _ := idOf(info)
	return info, id, names, nil
}

func entryID(path string, d fs.DirEntry) (fileID, error) {
	info, err := d.Info()
	if err != nil {
		return fileID{}, err
	}
	id, _, ok := idOf(info)
	if !ok {
		return fileID{}, &fs.PathError{Op: "stat", Path: path, Err: syscall.EINVAL}
	}
	return id, nil
}
