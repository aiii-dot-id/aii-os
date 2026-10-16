//go:build (!linux && !windows) || android || (!amd64 && !arm64)

package driver

import "errors"

func measure() (Capacity, error) {
	return Capacity{}, errors.New("Vulkan capacity is unavailable on this topology")
}
