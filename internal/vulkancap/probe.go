package vulkancap

import "path/filepath"

const Helper = "aii-vulkan-probe"

func HelperPath(exe string) string { return filepath.Join(filepath.Dir(exe), Helper) }

const Subcommand = "_hardware-vulkan"

const (
	ExitLoaderUnavailable       = 10
	ExitNoDiscreteDevice        = 11
	ExitAmbiguousDevices        = 12
	ExitMemoryBudgetUnavailable = 13
)

func FailureReason(status int) string {
	switch status {
	case ExitLoaderUnavailable:
		return "the system Vulkan loader could not be opened"
	case ExitNoDiscreteDevice:
		return "no supported discrete Vulkan GPU was found; integrated and software devices are not measured"
	case ExitAmbiguousDevices:
		return "more than one discrete Vulkan GPU was found; device placement is not measured"
	case ExitMemoryBudgetUnavailable:
		return "the Vulkan driver does not expose the required memory-budget extension"
	}
	return ""
}

type Capacity struct {
	Total     int64 `json:"total"`
	Available int64 `json:"available"`
}
