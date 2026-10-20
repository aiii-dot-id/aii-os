package driver

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/aiii-dot-id/aii-os/internal/vulkancap"
)

type Capacity = vulkancap.Capacity

var (
	errLoaderUnavailable       = errors.New("Vulkan loader unavailable")
	errNoDiscreteDevice        = errors.New("Vulkan discrete GPU unavailable")
	errAmbiguousDevices        = errors.New("Vulkan multi-discrete-GPU placement is unknown")
	errMemoryBudgetUnavailable = errors.New("Vulkan driver does not expose a memory budget")
)

func Run(out, diagnostic io.Writer) int {
	return run(out, diagnostic, measure)
}

func run(out, diagnostic io.Writer, measure func() (Capacity, error)) int {
	c, err := measure()
	if err != nil {
		fmt.Fprintln(diagnostic, err)
		switch {
		case errors.Is(err, errLoaderUnavailable):
			return vulkancap.ExitLoaderUnavailable
		case errors.Is(err, errNoDiscreteDevice):
			return vulkancap.ExitNoDiscreteDevice
		case errors.Is(err, errAmbiguousDevices):
			return vulkancap.ExitAmbiguousDevices
		case errors.Is(err, errMemoryBudgetUnavailable):
			return vulkancap.ExitMemoryBudgetUnavailable
		}
		return 1
	}
	if err := json.NewEncoder(out).Encode(c); err != nil {
		return 1
	}
	return 0
}
