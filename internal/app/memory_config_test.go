package app

import (
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/memory/salience"
)

// memory.salience is the operator's policy; the defaults are the
// initial table and every config carries a version.
func TestMemorySalienceDefaults(t *testing.T) {
	cfg := defaultConfig()
	if cfg.Memory.Salience.Version != salience.DefaultSalience().Version || cfg.Memory.Salience.BeliefAt != salience.DefaultSalience().BeliefAt {
		t.Fatalf("defaults not applied: %+v", cfg.Memory.Salience)
	}
	cfg.Memory.Salience = salience.SalienceWeights{}
	applyDefaults(cfg)
	if cfg.Memory.Salience.Version == "" {
		t.Fatal("an unversioned policy takes the defaults")
	}
}
