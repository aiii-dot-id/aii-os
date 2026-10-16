//go:build !((linux && !android) || (darwin && !ios) || windows)

package main

import (
	"fmt"
	"io"

	"github.com/aiii-dot-id/aii-os/internal/uninstall"
)

func runUninstall(_ []string, stdout, _ io.Writer) int {
	fmt.Fprintln(stdout, uninstall.OSUninstall)
	return 0
}
