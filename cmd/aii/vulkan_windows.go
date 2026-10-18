//go:build windows

package main

import (
	"io"

	"github.com/aiii-dot-id/aii-os/internal/vulkancap/driver"
)

func runVulkanProbe(_ []string, stdout, stderr io.Writer) int { return driver.Run(stdout, stderr) }
