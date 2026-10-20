package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/pluginfacility"
	"github.com/aiii-dot-id/aii-os/internal/vulkancap"
)

// vulkanSet is a component set that needs Vulkan, beside smallSet, which
// needs nothing.
var vulkanSet = componentSet{host: 2 << 30, device: 1 << 30, required: []string{"vulkan"}}

func measuredHost() pluginfacility.Availability {
	return pluginfacility.Availability{HostKnown: true, HostTotal: 32 << 30, HostAvailable: 16 << 30}
}

// THE LINUX INSTALL IS TWO FILES, AND THE SECOND CAN BE MISSING. An identity
// installed by copying aii alone ran its speech engine on the CPU, and the
// only word for it anywhere was the one a machine with no GPU gets:
// "accelerator:vulkan availability is unknown". The measurement now names the
// one cause it can see, the helper file not being there. Run as Linux's
// measurement on any host, like the deadline test beside it.
func TestTheMeasurementNamesAMissingVulkanProbeOnLinux(t *testing.T) {
	exe, args := selectionProbeFixture(t)
	missing := filepath.Join(t.TempDir(), vulkancap.Helper)
	at := func(path string, args ...string) func() (string, []string, error) {
		return func() (string, []string, error) { return path, args, nil }
	}
	want := pluginfacility.DeviceAvailability{Why: "the Vulkan probe (aii-vulkan-probe) is not installed beside aii"}

	a, err := measureSelectionFacts(t.Context(), "linux", unhurriedQuery, measuredHost, at(missing))
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := a.Devices["vulkan"]; !ok || got != want {
		t.Fatalf("a missing probe on Linux measured %+v (present %v), want %+v", got, ok, want)
	}
	if !a.HostKnown {
		t.Fatal("the host's own figures did not stand")
	}

	// A probe that is there and fails has a generic failure, never the
	// missing-file diagnosis or invented device figures.
	t.Setenv("AII_TEST_SELECTION_PROBE", "failure")
	a, err = measureSelectionFacts(t.Context(), "linux", unhurriedQuery, measuredHost, at(exe, args...))
	if err != nil {
		t.Fatal(err)
	}
	if got := a.Devices["vulkan"]; got != (pluginfacility.DeviceAvailability{Why: "the Vulkan probe could not run to completion"}) {
		t.Fatalf("a probe that is installed and failed has the wrong reason: %+v", got)
	}

	// A file that is there and cannot be started (no execute bit) is not a
	// missing file: the start fails, and the sentence is not said.
	blocked := filepath.Join(t.TempDir(), vulkancap.Helper)
	if err := os.WriteFile(blocked, []byte("not a program"), 0o600); err != nil {
		t.Fatal(err)
	}
	a, err = measureSelectionFacts(t.Context(), "linux", unhurriedQuery, measuredHost, at(blocked))
	if err != nil {
		t.Fatal(err)
	}
	if got := a.Devices["vulkan"]; got != (pluginfacility.DeviceAvailability{Why: "the Vulkan probe could not run to completion"}) {
		t.Fatalf("a probe file that is there has the wrong reason: %+v", got)
	}

	// Windows re-runs aii and has no second file: the sentence is Linux's.
	a, err = measureSelectionFacts(t.Context(), "windows", unhurriedQuery, measuredHost, at(missing))
	if err != nil {
		t.Fatal(err)
	}
	if got := a.Devices["vulkan"]; got != (pluginfacility.DeviceAvailability{Why: "the Vulkan probe could not run to completion"}) {
		t.Fatalf("the Linux helper was named on Windows or its failure was lost: %+v", got)
	}

	// And a probe that answers is read as before, with no reason on it.
	t.Setenv("AII_TEST_SELECTION_PROBE", "valid")
	a, err = measureSelectionFacts(t.Context(), "linux", unhurriedQuery, measuredHost, at(exe, args...))
	if err != nil || a.Devices["vulkan"] != (pluginfacility.DeviceAvailability{Known: true, Total: 8192, Available: 4096}) {
		t.Fatalf("an answering probe: %+v, %v", a.Devices, err)
	}
}

// AND THE OPERATOR READS IT. From the measurement, through the selection the
// runtime makes for a package with a Vulkan set and a CPU set, into the log
// line that says which set was chosen and why the other was passed over.
func TestAMissingVulkanProbeIsNamedWhereTheOperatorReadsWhy(t *testing.T) {
	logs := logsink.CaptureForTest(t)
	roots, pkg := componentSetsPackage(t, "1.0.0", vulkanSet, smallSet)
	missing := filepath.Join(t.TempDir(), vulkancap.Helper)
	a := selectionApp(t, roots, measuredHost, nil)
	a.pluginOpts.ResourceFacts = func(ctx context.Context) (pluginfacility.Availability, error) {
		return measureSelectionFacts(ctx, "linux", unhurriedQuery, measuredHost, func() (string, []string, error) { return missing, nil, nil })
	}
	h := newPluginRuntime(a)
	ev, err := h.Verify(t.Context(), pkg)
	if err != nil {
		t.Fatal(err)
	}
	p, err := h.Prepare(t.Context(), ev)
	if err != nil || p.HostBytes != smallSet.host {
		t.Fatalf("the CPU set was not chosen: %+v, %v", p, err)
	}
	const why = "accelerator:vulkan availability is unknown: the Vulkan probe (aii-vulkan-probe) is not installed beside aii"
	if p.Selection == nil || len(p.Selection.Excluded) != 1 || p.Selection.Excluded[0].Variant != "full" ||
		strings.Join(p.Selection.Excluded[0].Reasons, "; ") != why {
		t.Fatalf("the selection does not say why the Vulkan set was passed over: %+v", p.Selection)
	}
	for _, line := range logs.Lines() {
		if strings.Contains(line, why) && strings.Contains(line, "full") && strings.Contains(line, "small") {
			return
		}
	}
	t.Fatalf("no log line names the missing probe:\n%s", logs.String())
}
