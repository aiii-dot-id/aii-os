package app

import (
	"os"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/vulkancap"
)

func shippedVulkanProbe() (string, []string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", nil, err
	}
	if packagefmt.HostPlatform() == "windows" {
		return exe, []string{vulkancap.Subcommand}, nil
	}
	return vulkancap.HelperPath(exe), nil, nil
}
