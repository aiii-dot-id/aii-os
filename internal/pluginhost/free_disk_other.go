//go:build !windows && !(linux && !android) && !(darwin && !ios)

package pluginhost

import (
	"fmt"
	"runtime"
)

func freeDiskAt(string) (int64, error) {
	return 0, fmt.Errorf("free disk is not measured on %s", runtime.GOOS)
}
