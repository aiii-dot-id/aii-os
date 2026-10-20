package app

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/pluginfacility"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
	"github.com/aiii-dot-id/aii-os/internal/vulkancap"
)

const privateProbeText = "private-probe-marker /synthetic/untrusted/model.bin token=synthetic-secret\x1b[31m"

func diagnosticProbe(t *testing.T, mode string, status int) func() (string, []string, error) {
	t.Helper()
	exe, args := selectionProbeFixture(t)
	t.Setenv("AII_TEST_SELECTION_PROBE", mode)
	t.Setenv("AII_TEST_SELECTION_PROBE_STATUS", fmt.Sprint(status))
	t.Setenv("AII_TEST_SELECTION_PROBE_PRIVATE", privateProbeText)
	return func() (string, []string, error) { return exe, args, nil }
}

// Failure status is the only driver diagnostic admitted into operator text.
// The same failed subprocess writes valid capacity and private-looking stderr:
// neither is interpreted as room or quoted as a reason.
func TestVulkanProbeFailuresUseOnlyClosedReasons(t *testing.T) {
	for _, tc := range []struct {
		name, mode string
		status     int
		query      time.Duration
		why        string
	}{
		{"loader", "diagnostic", vulkancap.ExitLoaderUnavailable, unhurriedQuery, "the system Vulkan loader could not be opened"},
		{"no discrete GPU", "diagnostic", vulkancap.ExitNoDiscreteDevice, unhurriedQuery, "no supported discrete Vulkan GPU was found; integrated and software devices are not measured"},
		{"multiple discrete GPUs", "diagnostic", vulkancap.ExitAmbiguousDevices, unhurriedQuery, "more than one discrete Vulkan GPU was found; device placement is not measured"},
		{"memory budget", "diagnostic", vulkancap.ExitMemoryBudgetUnavailable, unhurriedQuery, "the Vulkan driver does not expose the required memory-budget extension"},
		{"older probe", "diagnostic", 1, unhurriedQuery, "the Vulkan probe could not run to completion"},
		{"unknown status", "diagnostic", 77, unhurriedQuery, "the Vulkan probe could not run to completion"},
		{"invalid figures", "garbled", 0, unhurriedQuery, vulkanProbeUnreadable},
		{"oversized reply", "overflow", 0, unhurriedQuery, "the Vulkan probe could not run to completion"},
		{"timeout", "hang", 0, 300 * time.Millisecond, "the Vulkan probe did not answer within 300 ms (plugins.runtime.hardware_query_ms)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			probe := diagnosticProbe(t, tc.mode, tc.status)
			for _, platform := range []string{"linux", "windows"} {
				a, err := measureSelectionFacts(t.Context(), platform, tc.query, measuredHost, probe)
				if err != nil || !a.HostKnown || a.HostTotal != measuredHost().HostTotal || a.HostAvailable != measuredHost().HostAvailable {
					t.Fatalf("%s: host figures changed: %+v, %v", platform, a, err)
				}
				if got := a.Devices["vulkan"]; got != (pluginfacility.DeviceAvailability{Why: tc.why}) {
					t.Fatalf("%s: failed probe gave %+v, want unknown with %q", platform, got, tc.why)
				}
			}
		})
	}
	t.Run("success ignores private stderr", func(t *testing.T) {
		probe := diagnosticProbe(t, "diagnostic", 0)
		for _, platform := range []string{"linux", "windows"} {
			a, err := measureSelectionFacts(t.Context(), platform, unhurriedQuery, measuredHost, probe)
			if err != nil || a.Devices["vulkan"] != (pluginfacility.DeviceAvailability{Known: true, Total: 8192, Available: 4096}) {
				t.Fatalf("%s: untrusted stderr changed a successful capacity reply: %+v %v", platform, a, err)
			}
		}
	})
}

func TestVulkanProbeMeasurementEndAndLookupFailureStayPrivate(t *testing.T) {
	probe := diagnosticProbe(t, "hang", 0)
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	a, err := measureSelectionFacts(ctx, "linux", unhurriedQuery, measuredHost, probe)
	if err != nil || a.Devices["vulkan"] != (pluginfacility.DeviceAvailability{Why: "the Vulkan probe did not finish before the selection measurement ended"}) {
		t.Fatalf("measurement deadline: %+v, %v", a, err)
	}
	a, err = measureSelectionFacts(t.Context(), "windows", unhurriedQuery, measuredHost, func() (string, []string, error) {
		return "", nil, errors.New(privateProbeText)
	})
	if err != nil || a.Devices["vulkan"] != (pluginfacility.DeviceAvailability{Why: "the Vulkan probe could not be located"}) {
		t.Fatalf("lookup failure: %+v, %v", a, err)
	}
	// The OS start error contains this private-looking path; the card must not.
	missing := filepath.Join(t.TempDir(), "private-probe-marker")
	a, err = measureSelectionFacts(t.Context(), "windows", unhurriedQuery, measuredHost, func() (string, []string, error) {
		return missing, nil, nil
	})
	if err != nil || a.Devices["vulkan"] != (pluginfacility.DeviceAvailability{Why: "the Vulkan probe could not run to completion"}) {
		t.Fatalf("start failure: %+v, %v", a, err)
	}
	ctx, cancel = context.WithCancel(t.Context())
	cancel()
	a, err = measureSelectionFacts(ctx, "windows", unhurriedQuery, func() pluginfacility.Availability {
		t.Fatal("cancelled measurement read host capacity")
		return pluginfacility.Availability{}
	}, func() (string, []string, error) {
		t.Fatal("cancelled measurement located a probe")
		return "", nil, nil
	})
	if !errors.Is(err, context.Canceled) || a.HostKnown || a.Devices != nil {
		t.Fatalf("entry cancellation changed: %+v, %v", a, err)
	}
}

// Low host memory is independent of the new explanation.
// Real verified package selection still falls back only when CPU fits, and
// still refuses when neither declaration fits the available host memory.
func TestVulkanReasonPreservesCPUFallbackAndLowMemoryRefusal(t *testing.T) {
	logs := logsink.CaptureForTest(t)
	gpu := componentSet{host: 5637144576, device: 2 << 30, required: []string{"vulkan"}}
	cpu := componentSet{host: 5 << 30}
	roots, pkg := componentSetsPackage(t, "1.0.0", gpu, cpu)
	for _, tc := range []struct {
		name, mode string
		available  int64
		want       string
	}{
		{"CPU fits", "diagnostic", 16 << 30, "small"},
		{"GPU fits", "roomy", 16 << 30, "full"},
		{"low host memory", "diagnostic", 2 << 30, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			probe := diagnosticProbe(t, tc.mode, vulkancap.ExitNoDiscreteDevice)
			facts := func() pluginfacility.Availability {
				a := measuredHost()
				a.HostAvailable = tc.available
				return a
			}
			measured, err := measureSelectionFacts(t.Context(), "windows", unhurriedQuery, facts, probe)
			if err != nil {
				t.Fatal(err)
			}
			prepare := func(facts pluginfacility.Availability) (pluginfacility.Prepared, error) {
				a := selectionApp(t, roots, func() pluginfacility.Availability { return facts }, nil)
				a.pluginOpts.ResourceFacts = func(context.Context) (pluginfacility.Availability, error) { return facts, nil }
				h := newPluginRuntime(a)
				ev, err := h.Verify(t.Context(), pkg)
				if err != nil {
					t.Fatal(err)
				}
				return h.Prepare(t.Context(), ev)
			}
			before := measured
			before.Devices = maps.Clone(measured.Devices)
			maps.DeleteFunc(before.Devices, func(_ string, d pluginfacility.DeviceAvailability) bool { return !d.Known })
			p, err := prepare(measured)
			old, oldErr := prepare(before)
			withoutWhy := func(reasons []string) []string {
				out := slices.Clone(reasons)
				const unknown = "accelerator:vulkan availability is unknown"
				for i, reason := range out {
					if strings.HasPrefix(reason, unknown+": ") {
						out[i] = unknown
					}
				}
				return out
			}
			if tc.want == "" {
				var refusal, oldRefusal *pluginhost.VariantSelectionError
				if !errors.As(err, &refusal) || !errors.As(oldErr, &oldRefusal) || len(refusal.Refusals) != 2 || len(oldRefusal.Refusals) != 2 {
					t.Fatalf("low memory did not retain the per-set refusal: %+v, %v", p, err)
				}
				for i, r := range refusal.Refusals {
					if !strings.Contains(strings.Join(r.Missing, "; "), "memory_bytes needs") {
						t.Fatalf("memory shortfall lost for %s: %+v", r.VariantID, r.Missing)
					}
					if r.VariantID != oldRefusal.Refusals[i].VariantID || !slices.Equal(withoutWhy(r.Missing), oldRefusal.Refusals[i].Missing) {
						t.Fatalf("capacity refusal changed beyond its explanation: now %+v before %+v", refusal.Refusals, oldRefusal.Refusals)
					}
				}
				return
			}
			if err != nil || oldErr != nil || p.Selection == nil || old.Selection == nil || p.Selection.Variant != tc.want || old.Selection.Variant != tc.want || p.HostBytes != old.HostBytes || p.Backend != old.Backend || p.DeviceBytes == nil || old.DeviceBytes == nil || *p.DeviceBytes != *old.DeviceBytes || len(p.Selection.Excluded) != len(old.Selection.Excluded) {
				t.Fatalf("selection or reservation changed: now %+v (%v) before %+v (%v), want %s", p, err, old, oldErr, tc.want)
			}
			for i, excluded := range p.Selection.Excluded {
				if excluded.Variant != old.Selection.Excluded[i].Variant || !slices.Equal(withoutWhy(excluded.Reasons), old.Selection.Excluded[i].Reasons) {
					t.Fatalf("excluded sets changed beyond their explanation: now %+v before %+v", p.Selection.Excluded, old.Selection.Excluded)
				}
			}
			if tc.want == "small" {
				const why = "accelerator:vulkan availability is unknown: no supported discrete Vulkan GPU was found; integrated and software devices are not measured"
				if len(p.Selection.Excluded) != 1 || len(p.Selection.Excluded[0].Reasons) != 1 || p.Selection.Excluded[0].Reasons[0] != why {
					t.Fatalf("safe reason did not reach selection: %+v", p.Selection)
				}
			}
		})
	}
	if strings.Contains(logs.String(), "private-probe-marker") || strings.Contains(logs.String(), "synthetic-secret") {
		t.Fatal("private-looking probe output leaked into the decision log")
	}
	if !strings.Contains(logs.String(), "no supported discrete Vulkan GPU was found") {
		t.Fatal("safe failure reason did not reach the decision log")
	}
}
