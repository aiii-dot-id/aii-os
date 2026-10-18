//go:build (linux && !android) || (darwin && !ios)

package uninstall

import (
	"os"
	"syscall"
)

func ownedHere(dir string) bool {
	fi, err := os.Stat(dir)
	if err != nil {
		return false
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	return ok && int(st.Uid) == os.Getuid()
}
