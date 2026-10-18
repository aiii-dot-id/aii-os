//go:build !windows

package genesis

import "syscall"

const netUnreachable = syscall.ENETUNREACH
