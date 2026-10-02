package main

import (
	"os"

	"github.com/aiii-dot-id/aii-os/internal/vulkancap/driver"
)

func main() { os.Exit(driver.Run(os.Stdout, os.Stderr)) }
