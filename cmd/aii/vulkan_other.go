//go:build !windows

package main

import (
	"fmt"
	"os"

	"github.com/aiii-dot-id/aii-os/internal/vulkancap"
)

func runVulkanProbe() int {
	fmt.Fprintf(os.Stderr, "aii: %s is the Windows Vulkan probe; on Linux the probe is %s, beside aii\n", vulkancap.Subcommand, vulkancap.Helper)
	return 2
}
