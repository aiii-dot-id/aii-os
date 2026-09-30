//go:build (!darwin && !windows && !linux) || ios || android

package main

import (
	"fmt"
	"os"
	"runtime"
)

func main() {
	fmt.Fprintf(os.Stderr, "aii-app is the desktop launcher; on %s run aii directly (see: aii init)\n", runtime.GOOS)
	os.Exit(1)
}
