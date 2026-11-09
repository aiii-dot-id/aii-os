//go:build (linux && !android) || (darwin && !ios) || windows

package pluginhost

// The Windows voice qualification's activation: the signed package as it is
// published, verified against the roots this build ships, the host's own
// selection on this machine's measured facts, production materialization
// from prepared files, and contained activation. Nothing is re-signed and
// nothing is downloaded.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/broker"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/pluginfacility"
	"github.com/aiii-dot-id/aii-os/internal/plugstore"
	"github.com/aiii-dot-id/aii-os/internal/store"
	"github.com/aiii-dot-id/aii-os/internal/version"
)

// journeyLog is the host's log of the journey that is running: what the host
// says and what the plugin's carrier and worker write to it.
var journeyLog atomic.Pointer[logsink.Capture]

// journeyPluginStore is the writer of the plugin store the running journey's
// plugin was activated from. A journey that brings more material for the
// running plugin asks this same writer: a store has one writer at a time.
var journeyPluginStore atomic.Pointer[plugstore.Writer]

// journeyRuntimePins is how many pins of this process hold the record of the
// runtime root at root in the store at dir: an activation pins its root's
// record for its life.
func journeyRuntimePins(dir, root string) int {
	if root == "" {
		return 0
	}
	return plugstore.HeldForTest(dir)[filepath.Clean(root)+plugstore.RecordSuffix]
}

// journeySlowStorageSaidFrom is the time from which the journeys' host says a
// plugin's storage call in its log (broker.Config.SlowStorageCall; the host's
// own default is one second). It changes what is logged and nothing else.
const journeySlowStorageSaidFrom = 250 * time.Millisecond

// journeyBudget is the operator's plugin memory budget of a budget row, and
// how it was come by: the middle of what the package's Small and Full sets
// declare of the host's memory, so that the host's selection admits Small
// and passes over Full. Where Full declares no more than Small, no budget
// separates them and the row refuses to run.
func journeyBudget(t *testing.T, expected string, profiles map[string]AcceleratorProfile) (int64, map[string]any) {
	t.Helper()
	base, isSmall := strings.CutSuffix(expected, "-small")
	if !isSmall {
		t.Fatalf("a budget row expects a system's Small set; this one expects %q", expected)
	}
	small, haveSmall := profiles[expected]
	full, haveFull := profiles[base+"-full"]
	if !haveSmall || !haveFull {
		t.Fatalf("the package declares no profile for %s or for %s-full", expected, base)
	}
	if full.MemoryBytes <= small.MemoryBytes {
		t.Fatalf("NO BUDGET SEPARATES THE SETS: %s declares %d bytes of host memory and %s-full declares %d; a budget that admits the first admits the second, and this row is not run", expected, small.MemoryBytes, base, full.MemoryBytes)
	}
	budget := small.MemoryBytes + (full.MemoryBytes-small.MemoryBytes)/2
	return budget, map[string]any{"small_set": expected, "small_declares_memory_bytes": small.MemoryBytes,
		"full_set": base + "-full", "full_declares_memory_bytes": full.MemoryBytes,
		"budget_is": "the middle of the two declarations", "budget_bytes": budget}
}

// capacityFacts is the host's production capacity measurement, made by the
// internal/app test program at probe (TestVoiceQualificationCapacity), which
// this package cannot import. It is measured again at every selection.
func capacityFacts(probe string) func(context.Context) (pluginfacility.Availability, error) {
	return func(ctx context.Context) (pluginfacility.Availability, error) {
		var facts pluginfacility.Availability
		cmd := exec.CommandContext(ctx, probe, "-test.run=^TestVoiceQualificationCapacity$", "-test.v")
		cmd.Env = append(os.Environ(), capacityProbeAsked+"=1")
		raw, err := cmd.CombinedOutput()
		if err != nil {
			return facts, fmt.Errorf("production capacity measurement: %w: %s", err, raw)
		}
		for _, line := range strings.Split(string(raw), "\n") {
			if after, ok := strings.CutPrefix(strings.TrimSpace(line), "CAPACITY="); ok {
				return facts, json.Unmarshal([]byte(after), &facts)
			}
		}
		return facts, fmt.Errorf("production capacity measurement returned no facts: %s", raw)
	}
}

func journeyCopy(t *testing.T, src, dest string) {
	t.Helper()
	in, err := os.Open(src)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dest), 0700); err != nil {
		t.Fatal(err)
	}
	out, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil || closeErr != nil {
		t.Fatalf("copy: %v %v", copyErr, closeErr)
	}
}

// activateVoice activates the row's signed package and writes the row's
// activation receipt (<test>-activation.json) when the test ends.
func activateVoice(t *testing.T, r voiceRow) *ActivePlugin {
	t.Helper()
	raw, err := os.ReadFile(r.pkg)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(raw)
	if hex.EncodeToString(hash[:]) != r.sha256 {
		t.Fatal("the signed package changed after the run read it")
	}
	roots := packagefmt.ShippedRoots()
	roots.Revocation = packagefmt.LoadRevocationStatus(t.TempDir(), roots, nil)
	verified, err := packagefmt.VerifyFile(r.pkg, roots)
	if err != nil {
		t.Fatal(err)
	}
	if verified.Tier != packagefmt.TierT3 {
		t.Fatalf("not production T3: %v", verified.Tier)
	}
	member := func(name string) []byte {
		t.Helper()
		b, err := loadVerifiedMember(r.pkg, verified, nil, name)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	var variants []string
	for _, v := range verified.Manifest.Variants {
		variants = append(variants, v.VariantID)
	}
	profiles, err := ParseAccelerators(member(AcceleratorFile), variants)
	if err != nil {
		t.Fatal(err)
	}
	limits := packagefmt.DefaultTreeLimits
	policy := pluginfacility.AdmissionPolicy{}
	var budgetFrom map[string]any
	if r.budget {
		policy.BudgetBytes, budgetFrom = journeyBudget(t, r.expected, profiles)
	}
	runtimes, err := ParseRuntimes(member(RuntimesFile))
	if err != nil {
		t.Fatal(err)
	}
	hostVersion := version.Authored()
	facts := capacityFacts(r.probe)
	sel, err := SelectContext(t.Context(), r.pkg, verified, supervisedOpts(Options{HostVersion: hostVersion, RuntimeLimits: limits, ResourceFacts: facts, SelectionPolicy: policy}))
	if err != nil {
		t.Fatal("automatic selection", err)
	}
	variant := sel.VariantID
	if variant != r.expected {
		t.Fatalf("automatic selection %s, expected %s; exclusions %v", variant, r.expected, sel.ResourceRefusals)
	}
	profile, ok := profiles[variant]
	if !ok {
		t.Fatalf("missing platform profile %s", variant)
	}
	selected := map[string]bool{}
	for _, name := range profile.Models {
		selected[name] = true
	}
	declarations, err := ParseModels(member(ModelsFile))
	if err != nil {
		t.Fatal(err)
	}
	scratch := t.TempDir()
	modelDir := pluginStorageDir(filepath.Join(scratch, "models"), "id.aiii.voice")
	modelCount := 0
	for _, d := range declarations {
		if selected[d.Name] {
			journeyCopy(t, filepath.Join(r.models, filepath.FromSlash(d.Dest())), filepath.Join(modelDir, filepath.FromSlash(d.Dest())))
			modelCount++
		}
	}
	expectedModels := len(selected)
	if modelCount != expectedModels {
		t.Fatalf("selected models = %d, expected %d", modelCount, expectedModels)
	}
	runtimeCount := 0
	for _, d := range runtimes {
		if d.VariantID == variant {
			journeyCopy(t, filepath.Join(r.assets, filepath.Base(d.URL)), filepath.Join(pluginStorageDir(filepath.Join(scratch, "runtime"), "id.aiii.voice"), "archives", d.SHA256+".tar.gz"))
			runtimeCount++
		}
	}
	if runtimeCount != 1 {
		t.Fatalf("runtime count = %d", runtimeCount)
	}
	// The host's store, made as the tree's own helper makes it, at a path
	// this fixture knows (the store-held row holds its write lock).
	storePath := filepath.Join(t.TempDir(), "aii.db")
	brokerStore, err := store.NewForTest(storePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = brokerStore.Close() })
	// The application gives its broker a runner for the receipts of reads
	// answered first (app.runBackground: a goroutine a job, waited for at
	// shutdown); the journeys give theirs the same kind of runner, whose jobs
	// are kept while a row holds the store's lock (journeyKept).
	var deferred sync.WaitGroup
	host, err := broker.New(broker.Config{Store: brokerStore, SlowStorageCall: journeySlowStorageSaidFrom,
		Later: func(job func()) bool {
			deferred.Add(1)
			journeyKept.do(func() { defer deferred.Done(); job() })
			return true
		}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(deferred.Wait) // before the store closes
	capture := logsink.CaptureForTest(t)
	journeyLog.Store(capture)
	t.Cleanup(func() { journeyLog.Store(nil) })
	report := map[string]any{
		"system":                           runtime.GOOS + "/" + runtime.GOARCH,
		"host_code":                        "the host's packages compiled into this test program",
		"host_says_a_storage_call_from_ms": journeySlowStorageSaidFrom.Milliseconds(),
		"scope":                            "exact signed release, production host materialization and contained activation from verified preseeded assets; not public downloads, browser, or physical audio",
		"host_version":                     hostVersion, "signed_package_sha256": r.sha256,
		"variant": variant, "tier": verified.Tier, "model_files": modelCount,
		"fetchers_nil": true, "temporary_identity_state": true,
		"resource_facts":   "production collector in a qualification subprocess, measured again at every selection",
		"selection_policy": policy, "selection_exclusions": sel.ResourceRefusals,
		"operator_budget_from": budgetFrom,
		"started_utc":          time.Now().UTC().Format(time.RFC3339Nano),
	}
	// Registered before the process cleanup, so failures and retirement are captured.
	t.Cleanup(func() {
		said := journeySlowStorageCalls(capture.String())
		report["host_storage_calls_said_slow"] = said
		slowest := time.Duration(0)
		for _, call := range said {
			if d := time.Duration(call.Seconds * float64(time.Second)); d > slowest {
				slowest = d
			}
		}
		report["host_storage_call_slowest_seconds"] = slowest.Seconds()
		// A row that holds the store's write lock shows nothing unless a write
		// of the plugin's waited for it: the host must have said a call at
		// least this long.
		if r.slowAtLeast > 0 && slowest < r.slowAtLeast {
			t.Errorf("the host said no storage call of %s or more (the longest it said: %s): no write of the plugin's waited that long, and the row shows nothing of what it was run for", r.slowAtLeast, slowest)
		}
		report["passed"] = !t.Failed()
		if err := os.MkdirAll(r.evidence, 0700); err != nil {
			t.Error(err)
			return
		}
		name := filepath.Base(t.Name())
		if err := os.WriteFile(filepath.Join(r.evidence, name+"-activation.log"), []byte(capture.String()), 0600); err != nil {
			t.Error(err)
		}
		data, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			t.Error(err)
			return
		}
		if err := os.WriteFile(filepath.Join(r.evidence, name+"-activation.json"), append(data, '\n'), 0600); err != nil {
			t.Error(err)
		}
	})
	// Eight minutes: each row adopts about 4.9 GB into a fresh store before
	// the child starts, and a Full row on a Windows machine took 240 s.
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	// The plugin store: a fresh one under this test's scratch, its writer in
	// this process, adopting the prepared models and runtime archive by
	// digest, with no fetcher. Material the prepared folders do not hold
	// refuses the activation. Closed after the plugin is deactivated.
	pluginStore, err := plugstore.OpenWriter(filepath.Join(scratch, "plugins"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		journeyPluginStore.CompareAndSwap(pluginStore, nil)
		for path, n := range plugstore.HeldForTest(pluginStore.Dir) {
			t.Errorf("%d pin(s) still hold %s after the plugin was deactivated", n, path)
		}
		pluginStore.Close()
	})
	journeyPluginStore.Store(pluginStore)
	materialize := StoreMaterializerForTest(pluginStore)
	report["plugin_store"] = map[string]any{"store": "fresh, under the test's scratch; its writer in this process (StoreMaterializerForTest)",
		"adopted_from": "the prepared models and runtime archive (AdoptModelsDir, AdoptRuntimeDir)", "fetcher": nil}
	start := time.Now()
	report["runtime_limits"] = limits
	ap, err := Activate(ctx, r.pkg, newRegistry(t), supervisedOpts(Options{
		HostVersion: hostVersion, Roots: roots, Broker: host,
		ResourceFacts: facts, SelectionPolicy: policy,
		StoreDir: pluginStore.Dir,
		Materialize: func(ctx context.Context, m Material) error {
			return materialize(ctx, m, nil)
		},
		AdoptModelsDir: filepath.Join(scratch, "models"), AdoptRuntimeDir: filepath.Join(scratch, "runtime"),
		PluginDataDir: filepath.Join(scratch, "data"),
		// What the plugin's settings hold: the fixed values of every journey,
		// or what a journey stores while the plugin runs (journeyStored).
		Settings:      journeyStoredSettings,
		RuntimeLimits: limits,
	}))
	report["activation_seconds"] = time.Since(start).Seconds()
	if err != nil {
		report["error"] = err.Error()
		t.Fatalf("production signed activation: %v\n%s", err, capture.String())
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		err := ap.Deactivate(ctx)
		report["deactivation_error"] = fmt.Sprint(err)
		report["runtime_refs_after_deactivation"] = journeyRuntimePins(pluginStore.Dir, ap.RuntimeRoot)
		if err != nil {
			t.Error(err)
		}
		if journeyRuntimePins(pluginStore.Dir, ap.RuntimeRoot) != 0 {
			t.Error("runtime pin retained after deactivation")
		}
	})
	// The store held on purpose, from here to just before the plugin is
	// deactivated: registered after the deactivation above, so ended before it.
	if r.storeHold > 0 {
		held := journeyHoldStore(t, brokerStore, storePath, "id.aiii.voice", r.storeHold)
		t.Cleanup(func() { report["store_held"] = held.end() })
	}
	report["readiness"], report["containment"], report["runtime_root"] = ap.Readiness, ap.sup.Containment(), ap.RuntimeRoot
	report["carrier_pid"] = ap.sup.Pid()
	if !ap.Contained || !ap.sup.Containment().Isolated() {
		t.Fatal("production containment not established")
	}
	// The ready line names the set's speaking backend as the selected,
	// signed runtime profile states it: vulkan, or none where it speaks on
	// the processor.
	data, err := os.ReadFile(filepath.Join(ap.RuntimeRoot, "native-profile.json"))
	if err != nil {
		t.Fatal(err)
	}
	var execution struct {
		Backend string `json:"backend"`
	}
	if err := json.Unmarshal(data, &execution); err != nil {
		t.Fatal(err)
	}
	expectedReady := ""
	switch execution.Backend {
	case "cpu":
	case "vulkan":
		expectedReady = "vulkan"
	default:
		t.Fatalf("unknown signed TTS backend %q", execution.Backend)
	}
	report["declared_backend"], report["expected_readiness_backend"] = profile.Backend, expectedReady
	// Five models loaded is the engine's own rule for a ready engine.
	if ap.Readiness == nil || !ap.Readiness.Real() || ap.Readiness.ModelsLoaded != 5 || !(registeredReadyLine(ap.Readiness.Accelerator, expectedReady) || ap.Readiness.Accelerator == combinedReadyLine[expectedReady]) {
		t.Fatalf("unexpected readiness %+v: the line must name cpu, then registered providers in order, with speaking backend %q", ap.Readiness, expectedReady)
	}
	if ap.RuntimeRoot == "" || len(ap.Models) != expectedModels || ap.Voice.Load() == nil {
		t.Fatal("runtime/model/session binding lost")
	}
	if journeyRuntimePins(pluginStore.Dir, ap.RuntimeRoot) != 1 {
		t.Fatal("runtime not pinned")
	}
	return ap
}

// combinedReadyLine is the ready line's earlier form, keyed by the speaking
// backend, which published Windows runtimes still write.
var combinedReadyLine = map[string]string{"vulkan": "cpu_vulkan", "": "cpu"}

// registeredReadyLine holds a ready line to its registered form: "cpu", then
// providers from the fixed list in its order, each once; it names speaking
// (metal or vulkan) when given, and neither GPU speaking backend when not.
func registeredReadyLine(line, speaking string) bool {
	order := []string{"cuda", "directml", "vulkan", "metal", "coreml"}
	words := strings.Split(line, "+")
	if len(words) == 0 || words[0] != "cpu" {
		return false
	}
	next, seen := 0, map[string]bool{}
	for _, w := range words[1:] {
		at := -1
		for i := next; i < len(order); i++ {
			if order[i] == w {
				at = i
				break
			}
		}
		if at < 0 {
			return false
		}
		seen[w], next = true, at+1
	}
	if speaking != "" {
		return seen[speaking]
	}
	return !seen["metal"] && !seen["vulkan"]
}
