//go:build !darwin && !windows

package main

import (
	"fmt"
	"os"
	"runtime"
)

// .
// .
// .
// .
func main() {
	fmt.Fprintf(os.Stderr, "aii-app is the macOS .app entry point; on %s run aii directly (see: aii init)\n", runtime.GOOS)
	os.Exit(1)
}
