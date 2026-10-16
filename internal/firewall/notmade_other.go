//go:build !windows

package firewall

import "syscall"

var openNothing = []error{syscall.ENOTDIR, syscall.ELOOP, syscall.ENAMETOOLONG}
