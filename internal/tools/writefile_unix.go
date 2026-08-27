//go:build !windows

package tools

import (
	"fmt"
	"os"
	"syscall"
)

// writeFileNoFollow is os.WriteFile with O_NOFOLLOW on the FINAL path
// component (H3 hardening): between validation resolving a path and the
// tool opening it, the final component can be swapped for a symlink —
// the classic check/use race. The kernel refusing to follow closes it.
// The sacrificed convenience is writing THROUGH an in-sandbox symlink;
// the error says so instead of leaking a bare ELOOP.
func writeFileNoFollow(path string, data []byte, perm os.FileMode) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC|syscall.O_NOFOLLOW, perm)
	if err != nil {
		if pe, ok := err.(*os.PathError); ok && pe.Err == syscall.ELOOP {
			return fmt.Errorf("%s is a symlink — writes go to real files only (symlink targets can move between check and use)", path)
		}
		return err
	}
	_, werr := f.Write(data)
	cerr := f.Close()
	if werr != nil {
		return werr
	}
	return cerr
}
