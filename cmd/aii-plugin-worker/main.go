// .
// .
// .
// .
package main

import (
	"os"

	"github.com/aiii-dot-id/aii-os/internal/workercmd"
)

func main() { os.Exit(workercmd.Run(os.Args[1:])) }
