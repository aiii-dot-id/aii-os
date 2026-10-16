//go:build windows

package firewall

import "golang.org/x/sys/windows"

var openNothing = []error{windows.ERROR_CANT_RESOLVE_FILENAME, windows.ERROR_DIRECTORY, windows.ERROR_FILENAME_EXCED_RANGE}
