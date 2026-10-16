//go:build windows

package pluginhost

import "syscall"

const hostPathLimit = syscall.MAX_PATH - 1
