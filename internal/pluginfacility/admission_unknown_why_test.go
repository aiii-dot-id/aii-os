package pluginfacility

import (
	"strings"
	"testing"
)

// A DEVICE THAT SAYS WHY IT IS UNKNOWN IS STILL UNKNOWN. An entry that is
// not Known may carry the cause (the probe that reads the device is not
// installed), and its figures are zero. Admission must read it as it reads
// no entry at all: one start on the device at a time, residency not bounded.
// Read as a measurement it would be a device with nothing free, and an
// engine that declares device memory would wait for room that never comes.
func TestADeviceThatSaysWhyItIsUnknownIsAdmittedAsUnmeasured(t *testing.T) {
	rt := &backendRuntime{sharedRuntime: newSharedRuntime(),
		backendFor: map[string]string{"engine.aiiospkg": "cpu+vulkan"}, deviceFor: map[string]int64{"engine.aiiospkg": 4 * gib}}
	rt.hostBytesFor["engine.aiiospkg"] = gib
	f := facilityWith(t, rt, deviceCapacity{"vulkan": {Why: "the probe is not installed"}})
	f.Observe(obs("engine.aiiospkg"), Policy{Revision: 1})
	until(t, "the engine to serve, admitted on a device nobody measured", func() bool {
		v := viewOf(f, idOf("engine.aiiospkg"))
		return v.State == StateActive && strings.Contains(v.Admission, "vulkan capacity is not measured")
	})
}
