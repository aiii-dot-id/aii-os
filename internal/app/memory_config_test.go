package app

import (
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/memory"
)

// .
// .
func TestMemorySalienceDefaults(t *testing.T) {
	cfg := defaultConfig()
	if cfg.Memory.Salience.Version != memory.DefaultSalience.Version || cfg.Memory.Salience.BeliefAt != memory.DefaultSalience.BeliefAt {
		t.Fatalf("defaults not applied: %+v", cfg.Memory.Salience)
	}
	cfg.Memory.Salience = memory.SalienceWeights{}
	applyDefaults(cfg)
	if cfg.Memory.Salience.Version == "" {
		t.Fatal("an unversioned policy takes the defaults")
	}
}
