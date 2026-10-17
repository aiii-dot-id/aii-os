package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/pluginfacility"
	"github.com/aiii-dot-id/aii-os/internal/tools"
)

// unhurriedQuery is a query's bound for a test that is not about the bound:
// no fixture here takes it, however busy the machine that runs them. The
// default, two seconds, was once not enough for a fixture's own start.
const unhurriedQuery = 30 * time.Second

func TestSelectionProbeProcess(t *testing.T) {
	switch os.Getenv("AII_TEST_SELECTION_PROBE") {
	case "hang":
		time.Sleep(30 * time.Second)
		os.Exit(0)
	case "failure":
		os.Exit(3)
	case "overflow":
		fmt.Print(strings.Repeat("x", 65537))
		os.Exit(0)
	case "valid":
		fmt.Print(`{"total":8192,"available":4096}`)
		os.Exit(0)
	case "slow": // answers, after the time it is told to take
		d, err := time.ParseDuration(os.Getenv("AII_TEST_SELECTION_PROBE_TAKES"))
		if err != nil {
			os.Exit(4)
		}
		time.Sleep(d)
		fmt.Print(`{"total":8192,"available":4096}`)
		os.Exit(0)
	}
}

// The fixture has no asynchronous work to wait for at exit. Do not spend the
// hardware query's time on the race runtime's exit sleep; the
// child stays instrumented, and the parent's already running detector is
// unchanged. os.Exit in the child also skips TestMain's cleanup, so any setup
// it performs belongs to the parent test's temporary directory.
func selectionProbeFixture(t *testing.T) (string, []string) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("GORACE", os.Getenv("GORACE")+" atexit_sleep_ms=0")
	dir := t.TempDir()
	for _, name := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(name, dir)
	}
	return exe, []string{"-test.run=^TestSelectionProbeProcess$"}
}

// A probe that answers, fails or says too much is read as that whatever the
// machine's load, so each has a bound it cannot meet; the one that hangs for
// thirty seconds is cut at the bound it is given.
func TestSelectionProbeFailureAndDeadline(t *testing.T) {
	exe, args := selectionProbeFixture(t)
	for _, mode := range []string{"valid", "failure", "overflow", "hang"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("AII_TEST_SELECTION_PROBE", mode)
			bound := unhurriedQuery
			if mode == "hang" {
				bound = 300 * time.Millisecond
			}
			start := time.Now()
			raw, err := selectionQuery(t.Context(), bound, exe, args...)
			if mode == "valid" {
				if _, ok := parseVulkanCapacity(raw); err != nil || !ok {
					t.Fatalf("%s %v", raw, err)
				}
			} else if err == nil {
				t.Fatal("failed probe reported success")
			}
			if mode == "hang" && time.Since(start) > 6*time.Second {
				t.Fatal("probe lifetime was not bounded")
			}
		})
	}
}

// THE OPERATOR'S NUMBER IS THE BOUND IN FORCE. It was two seconds in the
// code, whatever the machine, and the plugin host held the whole measurement
// to five of its own. No number takes the default, and a number outside the
// range is refused by its key, at load and at the settings door. What the
// door takes is the time of the host's next query, read by the measuring
// function the plugin host is given: a probe that answers in one second is
// cut at 200 ms, and one that takes longer than the old five seconds answers
// at thirty.
func TestAHardwareQueryTakesTheOperatorsTime(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	load := func(runtime string) (*Config, error) {
		t.Helper()
		if err := os.WriteFile(path, []byte(`{"plugins":{"runtime":{`+runtime+`}}}`), 0o600); err != nil {
			t.Fatal(err)
		}
		return LoadConfig(path)
	}
	for _, runtime := range []string{``, `"hardware_query_ms":0`} {
		if cfg, err := load(runtime); err != nil || cfg.Plugins.Runtime.hardwareQuery() != 2*time.Second {
			t.Errorf("{%s}: no number takes two seconds: %v", runtime, err)
		}
	}
	for _, ms := range []string{"100", "60000"} {
		if _, err := load(`"hardware_query_ms":` + ms); err != nil {
			t.Errorf("%s is in the range and was refused: %v", ms, err)
		}
	}
	for _, ms := range []string{"99", "60001", "-1"} {
		if _, err := load(`"hardware_query_ms":` + ms); err == nil || !strings.Contains(err.Error(), "plugins.runtime.hardware_query_ms") || !strings.Contains(err.Error(), "between 100 and 60000") {
			t.Errorf("%s must be refused by its key and its range: %v", ms, err)
		}
	}
	// The whole measurement follows the query: the five seconds it always
	// had at the default, and room for two whole queries at any other.
	if got := selectionMeasurement(PluginRuntimeConfig{}.hardwareQuery()); got != 5*time.Second {
		t.Errorf("at the default a whole measurement is held to %s; it has always had five seconds", got)
	}
	if q := 30 * time.Second; selectionMeasurement(q) <= 2*q {
		t.Errorf("a whole measurement of %s does not hold two queries of %s and the host's own read", selectionMeasurement(q), q)
	}

	cfg := defaultConfig()
	cfg.SourcePath = path
	cfg.Identity.LedgerPath = filepath.Join(t.TempDir(), "identity.ledger")
	if _, err := saveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	a := New(cfg)
	opts, err := a.buildPluginOptions(newAdmissionStore(t, t.TempDir()), tools.NewRegistry(t.TempDir(), nil, tools.Timeouts{}), nil)
	if err != nil {
		t.Fatal(err)
	}
	a.pluginOpts = opts
	const key = "plugins.runtime.hardware_query_ms"
	if _, err := a.applyConfigChange(map[string]any{key: 99.0}); err == nil || !strings.Contains(err.Error(), key) {
		t.Errorf("the door must refuse 99 by its key: %v", err)
	}

	t.Run("the host's measurement", func(t *testing.T) {
		if packagefmt.HostPlatform() == "macos" {
			t.Skip("the measurement of a Mac asks no Vulkan probe, and its one query is the system's own tool: there is none a test can point at a fixture")
		}
		exe, args := selectionProbeFixture(t)
		shipped := vulkanProbe
		vulkanProbe = func() (string, []string, error) { return exe, args, nil }
		t.Cleanup(func() { vulkanProbe = shipped })
		t.Setenv("AII_TEST_SELECTION_PROBE", "slow")
		for _, tc := range []struct {
			ms    float64
			takes time.Duration
			want  pluginfacility.DeviceAvailability
		}{
			{200, time.Second, pluginfacility.DeviceAvailability{}},
			{30000, 5500 * time.Millisecond, pluginfacility.DeviceAvailability{Known: true, Total: 8192, Available: 4096}},
		} {
			if _, err := a.applyConfigChange(map[string]any{key: tc.ms}); err != nil {
				t.Fatal(err)
			}
			t.Setenv("AII_TEST_SELECTION_PROBE_TAKES", tc.takes.String())
			facts, err := opts.ResourceFacts(t.Context())
			if err != nil || !facts.HostKnown || facts.Devices["vulkan"] != tc.want {
				t.Errorf("hardware_query_ms %v, a probe that takes %s: measured %+v (%v), want the host's figures and %+v", tc.ms, tc.takes, facts, err, tc.want)
			}
		}
	})

	state, err := a.applyConfigChange(map[string]any{key: 30000.0})
	if err != nil {
		t.Fatal(err)
	}
	if got := a.configSnapshot().Plugins.Runtime.hardwareQuery(); got != 30*time.Second || slices.Contains(state.RestartRequired, key) {
		t.Errorf("the door's 30000: the bound is %s, and what waits for a restart is %v", got, state.RestartRequired)
	}
	if onDisk, err := LoadConfig(path); err != nil || onDisk.Plugins.Runtime.HardwareQueryMs != 30000 {
		t.Errorf("the door's number is not in the file: %v", err)
	}
	if _, err := a.applyConfigChange(map[string]any{key: nil}); err != nil || a.configSnapshot().Plugins.Runtime.hardwareQuery() != 2*time.Second {
		t.Errorf("cleared at the door, the bound is two seconds again: %v", err)
	}
}

func TestAcceleratorFactsDoNotInventMemoryOrDevices(t *testing.T) {
	for _, tc := range []struct {
		raw string
		ok  bool
	}{
		{`{"total":8192,"available":4096}`, true}, {`{"total":8192,"available":0}`, true},
		{`{"total":0,"available":0}`, false}, {`{"total":8192,"available":8193}`, false},
		{`{"total":8192,"available":-1}`, false}, {`{"total":8192,"available":4096} {}`, false},
		{`{"total":8192,"available":4096,"guessed":true}`, false}, {`broken`, false},
	} {
		if d, ok := parseVulkanCapacity([]byte(tc.raw)); ok != tc.ok || (ok && (!d.Known || d.Unified)) {
			t.Fatalf("%q: %+v %v", tc.raw, d, ok)
		}
	}
	for _, tc := range []struct {
		raw string
		ok  bool
	}{
		{"8192, 6144, Default", true}, {"8192, 0, Default", true},
		{"8192, 6144, Prohibited", false}, {"8192, N/A, Default", false},
		{"8192, 9000, Default", false}, {"0, 0, Default", false},
		{"8192, -1, Default", false}, {"9223372036854775807, 0, Default", false},
		{"8192, 6144, Default\n8192, 6144, Default", false},
	} {
		d, ok := parseNvidiaCapacity([]byte(tc.raw))
		if ok != tc.ok {
			t.Fatalf("%q known=%v want=%v", tc.raw, ok, tc.ok)
		}
		if ok && (!d.Known || d.Unified || d.Available > d.Total) {
			t.Fatal(d)
		}
	}
	for _, tc := range []struct {
		raw string
		ok  bool
	}{
		{`{"SPDisplaysDataType":[{"spdisplays_vendor":"sppci_vendor_Apple","spdisplays_mtlgpufamilysupport":"spdisplays_metal4"}]}`, true},
		{`{"SPDisplaysDataType":[{"spdisplays_vendor":"sppci_vendor_Apple","spdisplays_metal":"spdisplays_supported"}]}`, true},
		{`{"SPDisplaysDataType":[{"spdisplays_vendor":"sppci_vendor_AMD","spdisplays_metal":"spdisplays_supported"}]}`, false},
		{`{"SPDisplaysDataType":[{"spdisplays_vendor":"sppci_vendor_Apple","spdisplays_mtlgpufamilysupport":"spdisplays_metal_unsupported"}]}`, false},
		{`{"SPDisplaysDataType":[]}`, false}, {`broken`, false},
	} {
		if got := hasAppleMetal([]byte(tc.raw)); got != tc.ok {
			t.Fatalf("metal known=%v want=%v", got, tc.ok)
		}
	}
	var b limitedSelectionOutput
	if _, err := b.Write([]byte(strings.Repeat("x", 64<<10))); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Write([]byte("x")); err == nil || b.Len() != 64<<10 {
		t.Fatal("unbounded output")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := measureSelectionResources(ctx, unhurriedQuery); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestCurrentHostSelectionMemoryIsMeasured(t *testing.T) {
	a, err := measureSelectionResources(t.Context(), PluginRuntimeConfig{}.hardwareQuery())
	if err != nil {
		t.Fatal(err)
	}
	if !a.HostKnown || a.HostTotal <= 0 || a.HostAvailable < 0 || a.HostAvailable > a.HostTotal {
		t.Fatalf("memory availability not measured: %+v", a)
	}
	t.Logf("measured available host bytes=%d; accelerator domains=%d", a.HostAvailable, len(a.Devices))
}

// A QUERY CUT SHORT LEAVES ITS OWN FACT UNKNOWN, AND NOTHING ELSE. The
// measurement used to return the deadline as its error, which failed the
// whole selection — the set that needs no accelerator included — whenever a
// driver tool was slow. Run as Linux's measurement on any host, with the
// probe pointed at this package's fixture rather than at aii.
func TestAQueryCutShortLeavesOnlyItsFactUnknown(t *testing.T) {
	exe, args := selectionProbeFixture(t)
	probe := func() (string, []string, error) { return exe, args, nil }
	host := func() pluginfacility.Availability {
		return pluginfacility.Availability{HostKnown: true, HostTotal: 8 << 30, HostAvailable: 4 << 30}
	}
	// The probe answers, ten seconds from now, inside its own time: its fact
	// is unknown only because the caller's deadline cut it, and no clock is
	// read to say so.
	t.Setenv("AII_TEST_SELECTION_PROBE", "slow")
	t.Setenv("AII_TEST_SELECTION_PROBE_TAKES", "10s")
	ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
	defer cancel()
	a, err := measureSelectionFacts(ctx, "linux", unhurriedQuery, host, probe)
	if err != nil {
		t.Fatalf("a query cut short failed the measurement: %v", err)
	}
	if !a.HostKnown || a.Devices["vulkan"].Known {
		t.Fatalf("measured %+v; the host's figures stand and the probe cut short leaves its fact unknown", a)
	}
	t.Setenv("AII_TEST_SELECTION_PROBE", "valid")
	if a, err := measureSelectionFacts(t.Context(), "linux", unhurriedQuery, host, probe); err != nil || a.Devices["vulkan"] != (pluginfacility.DeviceAvailability{Known: true, Total: 8192, Available: 4096}) {
		t.Fatalf("an answering probe is read: %+v, %v", a.Devices, err)
	}
}

// THE TEST BINARY IS NOT AII. Measuring this host from a test must never run
// the binary under test as the Vulkan probe (see vulkanProbe).
func TestSelectionMeasurementNeverRunsTheTestBinary(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if path, _, err := vulkanProbe(); err == nil && path == exe {
		t.Fatal("the Vulkan probe is this test binary: measuring would run the whole suite in a child")
	}
}
