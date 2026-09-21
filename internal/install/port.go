package install

import (
	"encoding/json"
	"os"
)

// .
// .
// .
// .
// .
// .
// .
// .
// .
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
