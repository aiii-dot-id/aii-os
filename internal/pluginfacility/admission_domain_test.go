package pluginfacility

import (
	"context"
	"strings"
	"testing"
)

// A BACKEND NAME RESOLVES TO THE DEVICE DOMAINS IT RUNS ON (§6.4). A
// profile's backend may be a composition — "cpu+vulkan" runs on Vulkan,
// "cuda+vulkan" on both — and capacity is measured per domain, so a
// composed name looked up whole matched nothing. Its device reservation
// was never checked against a measured device, and its start was never
// serialized with another start on the same unmeasured device.

// backendRuntime is sharedRuntime with an accelerator profile per package.
// Its maps are written before the facility starts and only read after.
type backendRuntime struct {
	*sharedRuntime
	backendFor map[string]string
	deviceFor  map[string]int64
}

func (r *backendRuntime) Prepare(ctx context.Context, ev Evidence) (Prepared, error) {
	p, err := r.sharedRuntime.Prepare(ctx, ev)
	p.Backend = r.backendFor[ev.Package]
	if d, ok := r.deviceFor[ev.Package]; ok {
		p.DeviceBytes = &d
	}
	return p, err
}

// deviceCapacity is a host with ample memory and the given device figures.
type deviceCapacity map[string]DeviceAvailability

func (c deviceCapacity) Measure() Availability {
	return Availability{HostKnown: true, HostTotal: 64 * gib, HostAvailable: 60 * gib, Devices: c}
}

func TestAComposedBackendIsCheckedAgainstEachMeasuredDevice(t *testing.T) {
	for _, backend := range []string{"vulkan", "cpu+vulkan", "vulkan+cpu", "cuda+vulkan", "vulkan+cuda"} {
		t.Run(backend, func(t *testing.T) {
			rt := &backendRuntime{sharedRuntime: newSharedRuntime(),
				backendFor: map[string]string{"engine.aiiospkg": backend}, deviceFor: map[string]int64{"engine.aiiospkg": 4 * gib}}
			rt.hostBytesFor["engine.aiiospkg"] = gib
			f := facilityWith(t, rt, deviceCapacity{
				"cuda":   {Known: true, Total: 24 * gib, Available: 20 * gib},
				"vulkan": {Known: true, Total: 8 * gib, Available: 2 * gib},
			})
			f.Observe(obs("engine.aiiospkg"), Policy{Revision: 1})
			until(t, "the engine to wait for its device", func() bool {
				v := viewOf(f, idOf("engine.aiiospkg"))
				return v.State == StateAdmitting && strings.Contains(v.Admission, "waiting for 4.0 GB on vulkan")
			})
			if strings.Contains(rt.seen(), "start:engine.aiiospkg") {
				t.Fatalf("it started on a device with 2 GB free for the 4 GB it declares: %s", rt.seen())
			}
		})
	}
}

func TestOneStartAtATimeHoldsAcrossSpellingsOfADomain(t *testing.T) {
	for _, pair := range [][2]string{{"cuda", "cuda+vulkan"}, {"vulkan", "cpu+vulkan"}, {"cuda+vulkan", "vulkan+cuda"}} {
		t.Run(pair[0]+" beside "+pair[1], func(t *testing.T) {
			rt := &backendRuntime{sharedRuntime: newSharedRuntime(),
				backendFor: map[string]string{"one.aiiospkg": pair[0], "two.aiiospkg": pair[1]}}
			gate := make(chan struct{})
			rt.mu.Lock()
			rt.gate = gate
			rt.mu.Unlock()
			f := facilityWith(t, rt, deviceCapacity{}) // no device is measured
			f.Observe(obs("one.aiiospkg", "two.aiiospkg"), Policy{Revision: 1})
			until(t, "one start in flight and the other waiting on the shared device", func() bool {
				a, b := viewOf(f, idOf("one.aiiospkg")), viewOf(f, idOf("two.aiiospkg"))
				w := b
				if a.State == StateAdmitting {
					w = a
				}
				return (a.State == StateStarting || b.State == StateStarting) && w.State == StateAdmitting &&
					strings.Contains(w.Admission, "capacity is not measured, so one engine starts on it at a time")
			})
			close(gate)
			until(t, "both to serve, one after the other", func() bool {
				return viewOf(f, idOf("one.aiiospkg")).State == StateActive && viewOf(f, idOf("two.aiiospkg")).State == StateActive
			})
		})
	}
	// THE CONTROL: the rule is per device, not one start for the host. The
	// CPU is no device: a CPU engine starts beside a CUDA one, and beside
	// one whose composition shares nothing with it but the CPU.
	for _, pair := range [][2]string{{"cpu", "cuda"}, {"cpu", "cpu+vulkan"}} {
		t.Run(pair[0]+" beside "+pair[1], func(t *testing.T) {
			rt := &backendRuntime{sharedRuntime: newSharedRuntime(),
				backendFor: map[string]string{"one.aiiospkg": pair[0], "two.aiiospkg": pair[1]}}
			gate := make(chan struct{})
			rt.mu.Lock()
			rt.gate = gate
			rt.mu.Unlock()
			f := facilityWith(t, rt, deviceCapacity{})
			f.Observe(obs("one.aiiospkg", "two.aiiospkg"), Policy{Revision: 1})
			// Both admitted at once: each is in the runtime's Start, held
			// at the gate, before either has finished.
			until(t, "both in Start together", func() bool {
				seen := rt.seen()
				return strings.Contains(seen, "start:one.aiiospkg") && strings.Contains(seen, "start:two.aiiospkg")
			})
			close(gate)
		})
	}
}

// WHAT A DEVICE HOLDS IS CHARGED WHATEVER IT WAS ASKED FOR AS. An engine
// serving on Vulkan holds its reservation there; one composed with the CPU
// asks for the same device and is charged what the first already holds.
func TestADeviceReservationIsChargedAcrossSpellings(t *testing.T) {
	rt := &backendRuntime{sharedRuntime: newSharedRuntime(),
		backendFor: map[string]string{"one.aiiospkg": "vulkan", "two.aiiospkg": "cpu+vulkan"},
		deviceFor:  map[string]int64{"one.aiiospkg": 3 * gib / 2, "two.aiiospkg": gib}}
	f := facilityWith(t, rt, deviceCapacity{"vulkan": {Known: true, Total: 8 * gib, Available: 2 * gib}})
	f.Observe(obs("one.aiiospkg"), Policy{Revision: 1})
	until(t, "the first to serve", func() bool { return viewOf(f, idOf("one.aiiospkg")).State == StateActive })
	f.Observe(obs("one.aiiospkg", "two.aiiospkg"), Policy{Revision: 1})
	until(t, "the second to wait on what the first holds", func() bool {
		v := viewOf(f, idOf("two.aiiospkg"))
		return v.State == StateAdmitting && strings.Contains(v.Admission, "waiting for 1.0 GB on vulkan: 2.0 GB free, 1.5 GB reserved")
	})
}
