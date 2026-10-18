//go:build linux && !android

package pluginhost

import (
	"os"

	"golang.org/x/sys/unix"
)

func cloneFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	cerr := unix.IoctlFileClone(int(out.Fd()), int(in.Fd()))
	if err := out.Close(); cerr == nil {
		cerr = err
	}
	if cerr != nil {
		_ = os.Remove(dst)
	}
	return cerr
}
