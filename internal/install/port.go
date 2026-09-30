package install

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

func ConfiguredPort(slotDir string, n int) int {
	data, err := os.ReadFile(ConfigPathIn(slotDir))
	if err != nil {
		return Port(n)
	}
	var cfg struct {
		Dashboard struct {
			Port int `json:"port"`
		} `json:"dashboard"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil || cfg.Dashboard.Port <= 0 {
		return Port(n)
	}
	return cfg.Dashboard.Port
}

const nextPortSpan = 100

func NextFreePort(root string, from int, free func(port int) bool) (int, error) {
	slots, err := Slots(root)
	if err != nil {
		return 0, err
	}
	taken := make(map[int]bool, len(slots))
	for _, n := range slots {
		taken[ConfiguredPort(filepath.Join(root, SlotName(n)), n)] = true
	}
	for p := from + 1; p <= 65535 && p <= from+nextPortSpan; p++ {
		if !taken[p] && free(p) {
			return p, nil
		}
	}
	return 0, fmt.Errorf("no free port between %d and %d", from+1, min(from+nextPortSpan, 65535))
}
