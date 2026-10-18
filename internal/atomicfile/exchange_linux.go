//go:build linux || android

package atomicfile

import "golang.org/x/sys/unix"

func exchange(oldPath, newPath string) error {
	return unix.Renameat2(unix.AT_FDCWD, oldPath, unix.AT_FDCWD, newPath, unix.RENAME_EXCHANGE)
}
