package app

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"
)

// TestVoiceQualificationCapacity is the host's production capacity
// measurement, printed for internal/pluginhost's Windows voice qualification
// (TestWindowsVoiceQualification), which cannot import this package. It runs
// only when that qualification asks for it.
func TestVoiceQualificationCapacity(t *testing.T) {
	if os.Getenv("AII_VOICE_QUALIFICATION_CAPACITY") != "1" {
		t.Skip("run by internal/pluginhost's Windows voice qualification (AII_VOICE_QUALIFICATION_CAPACITY=1)")
	}
	// The shipped Vulkan probe, which this package's TestMain replaces.
	held := vulkanProbe
	vulkanProbe = shippedVulkanProbe
	t.Cleanup(func() { vulkanProbe = held })
	facts, err := measureSelectionResources(t.Context(), PluginRuntimeConfig{}.hardwareQuery())
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(facts)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("CAPACITY=%s\n", b)
}
