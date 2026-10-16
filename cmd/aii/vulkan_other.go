//go:build !windows

package main

import (
	"fmt"
	"io"

	"github.com/aiii-dot-id/aii-os/internal/vulkancap"
)

func runVulkanProbe(_ []string, _, stderr io.Writer) int {
	fmt.Fprintf(stderr, "aii: %s is the Windows Vulkan probe; on Linux the probe is %s, beside aii\n", vulkancap.Subcommand, vulkancap.Helper)
	return 2
}
