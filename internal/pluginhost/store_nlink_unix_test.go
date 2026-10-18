//go:build (linux && !android) || (darwin && !ios)

package pluginhost

import (
	"os"
	"syscall"
)

func nlink(path string) (uint64, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	return uint64(fi.Sys().(*syscall.Stat_t).Nlink), nil
}
