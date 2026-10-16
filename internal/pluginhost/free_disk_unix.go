//go:build (linux && !android) || (darwin && !ios)

package pluginhost

import (
	"math"

	"golang.org/x/sys/unix"
)

func freeDiskAt(path string) (int64, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return 0, err
	}
	free := uint64(st.Bavail) * uint64(st.Bsize)
	if free > math.MaxInt64 {
		return math.MaxInt64, nil
	}
	return int64(free), nil
}
