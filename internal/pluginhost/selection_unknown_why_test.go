package pluginhost

import (
	"slices"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/pluginfacility"
)

// WHY A DEVICE IS UNKNOWN IS SAID WHERE THE COLLECTOR KNOWS IT. A set that
// needs an accelerator the host cannot measure was passed over with
// "availability is unknown" whatever the cause, so an install missing the
// file that reads the device read exactly like a machine without the device.
func TestAnUnknownAcceleratorSaysWhyWhereTheCollectorKnows(t *testing.T) {
	device := int64(1 << 30)
	p := AcceleratorProfile{MemoryBytes: 1 << 30, DeviceMemoryBytes: &device, RequiredAccelerators: []string{"vulkan"}}
	host := func(devices map[string]pluginfacility.DeviceAvailability) pluginfacility.Availability {
		return pluginfacility.Availability{HostKnown: true, HostTotal: 32 << 30, HostAvailable: 16 << 30, Devices: devices}
	}
	const unknown = "accelerator:vulkan availability is unknown"
	for _, tc := range []struct {
		name    string
		devices map[string]pluginfacility.DeviceAvailability
		want    []string
	}{
		{"no entry", nil, []string{unknown}},
		{"an entry that says why", map[string]pluginfacility.DeviceAvailability{"vulkan": {Why: "the probe is not installed"}},
			[]string{unknown + ": the probe is not installed"}},
		{"an entry that is unknown and says nothing", map[string]pluginfacility.DeviceAvailability{"vulkan": {}}, []string{unknown}},
		// A reason belongs to an unknown device. Figures that contradict
		// themselves are unknown for their own cause, not for a stale one.
		{"known figures that do not hold", map[string]pluginfacility.DeviceAvailability{"vulkan": {Known: true, Total: 1 << 30, Available: 2 << 30, Why: "stale"}},
			[]string{unknown}},
		{"measured, with room", map[string]pluginfacility.DeviceAvailability{"vulkan": {Known: true, Total: 8 << 30, Available: 8 << 30}}, nil},
		// The reason is one device's: another device's entry lends none.
		{"another device says why", map[string]pluginfacility.DeviceAvailability{"cuda": {Why: "not this one"}}, []string{unknown}},
	} {
		fixed, momentary := capacityRefusals(p, host(tc.devices), pluginfacility.AdmissionPolicy{})
		if !slices.Equal(logs(fixed), tc.want) || len(momentary) != 0 {
			t.Errorf("%s: fixed %q momentary %q, want fixed %q and nothing momentary", tc.name, logs(fixed), logs(momentary), tc.want)
		}
	}
}
