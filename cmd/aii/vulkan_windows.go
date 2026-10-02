//go:build windows

package main

import (
	"os"

	"github.com/aiii-dot-id/aii-os/internal/vulkancap/driver"
)

func runVulkanProbe() int { return driver.Run(os.Stdout, os.Stderr) }
