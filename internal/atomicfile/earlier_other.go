//go:build !windows

package atomicfile

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"syscall"
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
	if src, err := os.Lstat(oldPath); err == nil && os.SameFile(src, st) {
		f.Close()
		return nil, nil
	}
	return &earlier{f}, nil
}

func namesOf(f *os.File) (uint64, error) {
	st, err := f.Stat()
	if err != nil {
		return 0, err
	}
	return namesOfInfo(st)
}

func namesOfInfo(info os.FileInfo) (uint64, error) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, fmt.Errorf("%s: no link count in its stat", info.Name())
	}
	return uint64(st.Nlink), nil
}

func openToEmpty(path string) (*os.File, error) {
	open := func() (*os.File, error) {
		return os.OpenFile(path, os.O_WRONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	}
	f, err := open()
	if err == nil {
		st, serr := f.Stat()
		if serr != nil {
			f.Close()
			return nil, serr
		}
		if !st.Mode().IsRegular() {
			f.Close()
			return nil, nil
		}
		return f, nil
	}
	info, lerr := os.Lstat(path)
	if lerr != nil || !info.Mode().IsRegular() {
		return nil, nil
	}
	if n, _ := namesOfInfo(info); n < 2 {
		return nil, nil
	}
	if errors.Is(err, fs.ErrPermission) {
		if err = ownerWritable(path, info); err == nil {
			if ownerWindow != nil {
				ownerWindow(path)
			}
			if f, err = open(); err == nil {

				st, serr := f.Stat()
				if serr == nil && st.Mode().IsRegular() && os.SameFile(st, info) {
					return f, nil
				}
				f.Close()
				err = errors.Join(serr, fmt.Errorf("%s changed while it was made writable", path))
			}
		}
	}
	return nil, fmt.Errorf("it has another name, and could not be opened to be emptied (%w); its other names are found by running find on its volume with -samefile %s (it is inode %d)", err, path, inodeOf(info))
}

func openToRemove(path string) (*os.File, error) {
	return openToEmpty(path)
}

var ownerWindow func(path string)

func inodeOf(info os.FileInfo) uint64 {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return uint64(st.Ino)
	}
	return 0
}
