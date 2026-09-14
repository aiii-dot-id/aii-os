//go:build !windows

package main

import (
	"fmt"
	"os"
	"runtime"
)

// .
// .
// .
func main() {
	fmt.Fprintf(os.Stderr, "aii-setup is the Windows installer; on %s use the platform's own package\n", runtime.GOOS)
	os.Exit(1)
}
