package app

import (
	"encoding/json"
	"testing"
)

// .
// .
// .
// .
// .
func TestHarvestWakeConfigGate(t *testing.T) {
	// .
	var cfg1 AgencyConfig
	if err := json.Unmarshal([]byte(`{"queue_workers":2}`), &cfg1); err != nil {
		t.Fatal(err)
	}
	if hw := cfg1.HarvestWake; hw != nil && !*hw {
		t.Fatalf("absent harvest_wake parsed as explicit false: %+v", cfg1.HarvestWake)
	}

	// .
	var cfg2 AgencyConfig
	if err := json.Unmarshal([]byte(`{"harvest_wake":true}`), &cfg2); err != nil {
		t.Fatal(err)
	}
	if hw := cfg2.HarvestWake; hw == nil || !*hw {
		t.Fatalf("explicit true not honored: %+v", cfg2.HarvestWake)
	}

	// .
	var cfg3 AgencyConfig
	if err := json.Unmarshal([]byte(`{"harvest_wake":false}`), &cfg3); err != nil {
		t.Fatal(err)
	}
	if hw := cfg3.HarvestWake; hw == nil || *hw {
		t.Fatalf("explicit false not honored: %+v", cfg3.HarvestWake)
	}

	// .
	// .
	gate := func(hw *bool) bool { return hw == nil || *hw }
	if !gate(cfg1.HarvestWake) {
		t.Fatal("gate: absent key must mean ON")
	}
	if !gate(cfg2.HarvestWake) {
		t.Fatal("gate: explicit true must be ON")
	}
	if gate(cfg3.HarvestWake) {
		t.Fatal("gate: explicit false must be OFF")
	}
}
