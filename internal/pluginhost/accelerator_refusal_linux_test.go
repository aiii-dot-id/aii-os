//go:build linux && !android

package pluginhost

import (
	"context"
	"os"
	"strings"
	"testing"
)

// A VULKAN SET ON A HOST WITHOUT VULKAN DEVICES IS REFUSED at the wall, with
// its reason, and is never run on the processor in its place. The cases
// above hold the rule on a fixture; this holds it at an activation, on a host
// that has no Vulkan device (the Linux VM). A host with one has nothing to
// refuse, and says so.
func TestAVulkanSetIsRefusedOnAHostWithoutItsDevices(t *testing.T) {
	if devices, err := linuxAcceleratorDevices(&AcceleratorProfile{Backend: "vulkan"}, os.DirFS("/dev")); err == nil {
		t.Skipf("this host admits Vulkan devices (%v): there is nothing to refuse", devices)
	}
	rig := newNativeRigWith(t, "org.example.vulkan-without-devices", "ready-real", 20000, nil, func(p *AcceleratorProfile) { p.Backend = "vulkan" })
	ap, err := Activate(context.Background(), rig.pkg, newRegistry(t), rig.opts)
	if err == nil {
		_ = ap.Deactivate(context.Background())
		t.Fatal("a Vulkan set activated on a host without Vulkan devices")
	}
	if !strings.Contains(err.Error(), "accelerator device admission: Vulkan hardware device nodes unavailable; software/CPU substitution is not device admission") {
		t.Fatalf("the refusal does not say why: %v", err)
	}
}
