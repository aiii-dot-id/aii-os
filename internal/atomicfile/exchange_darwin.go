//go:build darwin || ios

package atomicfile

import "golang.org/x/sys/unix"

func exchange(oldPath, newPath string) error {
	return unix.RenamexNp(oldPath, newPath, unix.RENAME_SWAP)
}
