package install

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/atomicfile"
	"path/filepath"
)

func ConfiguredPort(slotDir string, n int) int {
	d, ok := dashboardSection(slotDir)
	switch {
	case !ok:
		return Port(n)
	case d.Port > 0:
		return d.Port
	default:

		return DefaultPort
	}
}

type slotDashboard struct {
	Port             int `json:"port"`
	StartWaitSeconds int `json:"start_wait_seconds"`
	PortSearchSpan   int `json:"port_search_span"`
}

func dashboardSection(slotDir string) (slotDashboard, bool) {
	data, err := atomicfile.ReadFile(ConfigPathIn(slotDir))
	if err != nil {
		return slotDashboard{}, false
	}
	var cfg struct {
		Dashboard slotDashboard `json:"dashboard"`
	}

	var wrongType *json.UnmarshalTypeError
	if err := json.Unmarshal(data, &cfg); err != nil && !errors.As(err, &wrongType) {
		return slotDashboard{}, false
	}
	return cfg.Dashboard, true
}

const (
	DefaultPortSearchSpan = 100
	MinPortSearchSpan     = 1
	MaxPortSearchSpan     = 1000
)

func HeldPortSearchSpan(n int) int {
	return held(n, DefaultPortSearchSpan, MinPortSearchSpan, MaxPortSearchSpan)
}

func ConfiguredPortSearchSpan(slotDir string) int {
	d, _ := dashboardSection(slotDir)
	return HeldPortSearchSpan(d.PortSearchSpan)
}

func held(n, def, lo, hi int) int {
	switch {
	case n == 0:
		return def
	case n < lo:
		return lo
	case n > hi:
		return hi
	}
	return n
}

func NextFreePort(root string, from, span int, free func(port int) bool) (int, error) {
	slots, err := Slots(root)
	if err != nil {
		return 0, err
	}
	taken := make(map[int]bool, len(slots))
	for _, n := range slots {
		taken[ConfiguredPort(filepath.Join(root, SlotName(n)), n)] = true
	}
	for p := from + 1; p <= 65535 && p <= from+span; p++ {
		if !taken[p] && free(p) {
			return p, nil
		}
	}
	return 0, fmt.Errorf("no free port between %d and %d (dashboard.port_search_span is %d)", from+1, min(from+span, 65535), span)
}
