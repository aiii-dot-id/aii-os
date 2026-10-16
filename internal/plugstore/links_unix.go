//go:build (linux && !android) || (darwin && !ios)

package plugstore

import (
	"fmt"
	"os"
	"syscall"
)

func Links(path string) (uint64, error) {
	fi, err := os.Lstat(path)
	if err != nil {
		return 0, err
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, fmt.Errorf("%s: no link count", path)
	}
	return uint64(st.Nlink), nil
}
