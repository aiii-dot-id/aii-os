package app

import (
	"os"

	"github.com/aiii-dot-id/aii-os/internal/vulkancap"
	"github.com/aiii-dot-id/aii-os/internal/vulkancap/driver"
)

// On Windows the shipped Vulkan probe is this executable run again with the
// probe's subcommand (shippedVulkanProbe): answered here as aii answers it,
// for TestVoiceQualificationCapacity.
func init() {
	if len(os.Args) == 2 && os.Args[1] == vulkancap.Subcommand {
		os.Exit(driver.Run(os.Stdout, os.Stderr))
	}
}
