package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/plugincatalog"

	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt/packagetest"
	"github.com/aiii-dot-id/aii-os/internal/pluginfacility"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
	"github.com/aiii-dot-id/aii-os/internal/tools"
)

const componentSetsID = "com.example.component-sets"

// componentSet is one set's declared reservation.
type componentSet struct {
	host, device int64
	required     []string
}

var (
	fullSet  = componentSet{host: 8 << 30, device: 6 << 30, required: []string{"cuda"}}
	smallSet = componentSet{host: 1 << 30}
)

func (s componentSet) profile(model string) pluginhost.AcceleratorProfile {
	device := s.device
	return pluginhost.AcceleratorProfile{OS: packagefmt.HostPlatform(), Arch: packagefmt.HostArch(), Backend: "cpu", Precision: "fp32",
		Models: []string{model}, MemoryBytes: s.host, DeviceMemoryBytes: &device, RequiredAccelerators: s.required, SessionLimit: 1, Fallback: "none"}
}

// The platform role and each distinct package are signed once per test
// binary: SLH-DSA keygen and signing take seconds — about fourteen under the
// race detector — and a signed package is immutable (pluginhost's
// broker_e2e_test signer, the same reasoning). Each test gets its own copy of
// the bytes.
var signedComponentSets = struct {
	sync.Mutex
	role  *packagetest.Role
	roots packagefmt.TrustRoots
	raw   map[string][]byte
}{raw: map[string][]byte{}}

// componentSetsPackage is a native T3 package for this host with two
// component sets in the publisher's order, full before small, each naming
// its own model. Nothing in it is ever executed.
func componentSetsPackage(t *testing.T, version string, full, small componentSet) (packagefmt.TrustRoots, string) {
	t.Helper()
	s := &signedComponentSets
	s.Lock()
	defer s.Unlock()
	if s.role == nil {
		role, status, err := packagetest.NewPlatformRelease("aiii_test_platform_release")
		if err != nil {
			t.Fatal(err)
		}
		trust := t.TempDir() // read now; the status set does not go back to it
		if err := os.WriteFile(filepath.Join(trust, packagetest.StatusFilePlatform), status, 0o600); err != nil {
			t.Fatal(err)
		}
		s.roots = packagefmt.TrustRoots{PlatformRelease: role.Env}
		s.roots.Revocation = packagefmt.LoadRevocationStatus(trust, s.roots, nil)
		s.role = role
	}
	key := fmt.Sprintf("%s %+v %+v", version, full, small)
	raw, ok := s.raw[key]
	if !ok {
		raw = signComponentSets(t, s.role, version, full, small)
		s.raw[key] = raw
	}
	pkg := filepath.Join(t.TempDir(), componentSetsID+"-"+version+".aiiospkg")
	if err := os.WriteFile(pkg, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return s.roots, pkg
}

// componentModelBody is the bytes served at a component set's model
// address: five of them, each set's its own.
func componentModelBody(url string) []byte {
	if url == "https://example.invalid/full" {
		return []byte("fullm")
	}
	return []byte("small")
}

func signComponentSets(t *testing.T, role *packagetest.Role, version string, full, small componentSet) []byte {
	t.Helper()
	// Each set's model its own bytes: one blob in the plugin store is one
	// set's, and a set's download is its own.
	fullSum, smallSum := sha256.Sum256(componentModelBody("https://example.invalid/full")), sha256.Sum256(componentModelBody("https://example.invalid/small"))
	models, err := json.Marshal([]pluginhost.ModelDecl{
		{Name: "full-model", URL: "https://example.invalid/full", Size: 5, SHA256: hex.EncodeToString(fullSum[:])},
		{Name: "small-model", URL: "https://example.invalid/small", Size: 5, SHA256: hex.EncodeToString(smallSum[:])}})
	if err != nil {
		t.Fatal(err)
	}
	profiles, err := json.Marshal(map[string]pluginhost.AcceleratorProfile{"full": full.profile("full-model"), "small": small.profile("small-model")})
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{
		"interfaces/quarantine.probe.v1.schema.json": []byte(`{"interface":"quarantine.probe","v":1}`),
		"variants/full/child":                        []byte("not executed"),
		"variants/small/child":                       []byte("not executed"),
		pluginhost.ModelsFile:                        models,
		pluginhost.AcceleratorFile:                   profiles,
	}
	variant := func(id string) packagetest.VariantSpec {
		return packagetest.VariantSpec{ID: id, Platform: packagefmt.HostPlatform(), Arch: packagefmt.HostArch(), Topology: packagefmt.HostTopology(),
			Runtime: "native_t3_component", Profile: "platform_reserved", Entrypoint: "variants/" + id + "/child"}
	}
	manifest := packagetest.BuildManifestJSON(componentSetsID, version,
		[]packagetest.InterfaceSpec{{ID: "quarantine.probe", Version: 1, SchemaFile: "interfaces/quarantine.probe.v1.schema.json", Methods: []string{"ping"}}},
		[]packagetest.VariantSpec{variant("full"), variant("small")}, files,
		map[string]any{"aiios_min_version": packagefmt.VariantPreferenceMinHost, "default_variant": "full", "variant_preference": []string{"full", "small"}})
	spec := packagetest.PackageSpec{Root: componentSetsID + "-" + version, Manifest: manifest, InstallFiles: files}
	if err := role.SignT3(&spec); err != nil {
		t.Fatal(err)
	}
	return packagetest.Build(spec)
}

// roomy is a host on which the full set fits with room to spare.
func roomy() pluginfacility.Availability {
	return pluginfacility.Availability{HostKnown: true, HostTotal: 32 << 30, HostAvailable: 16 << 30,
		Devices: map[string]pluginfacility.DeviceAvailability{"cuda": {Known: true, Total: 8 << 30, Available: 8 << 30}}}
}

// selectionApp is an App whose runtime selects against the given facts; the
// worker binary only switches the supervised lane on — nothing executes it.
func selectionApp(t *testing.T, roots packagefmt.TrustRoots, facts func() pluginfacility.Availability, acq *pluginhost.Acquirer) *App {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return &App{cfg: &Config{}, pluginToolReg: tools.NewRegistry(t.TempDir(), nil, tools.Timeouts{}),
		pluginOpts: &pluginhost.Options{Roots: roots, WorkerBinary: exe, StoreDir: t.TempDir(), Acquirer: acq,
			ResourceFacts: func(context.Context) (pluginfacility.Availability, error) { return facts(), nil }}}
}

// AN UPDATE IS SELECTED BESIDE THE ENGINE IT REPLACES. What the serving
// release holds is in use now and comes back when the candidate takes over;
// measured without it, the engine's own footprint made its own set look
// unaffordable and every update stepped down to the small set.
func TestAnUpdateIsSelectedCreditingTheEngineItReplaces(t *testing.T) {
	roots, pkg := componentSetsPackage(t, "1.0.0", fullSet, smallSet)
	busy := func() pluginfacility.Availability {
		return pluginfacility.Availability{HostKnown: true, HostTotal: 32 << 30, HostAvailable: 6 << 30,
			Devices: map[string]pluginfacility.DeviceAvailability{"cuda": {Known: true, Total: 8 << 30, Available: 2 << 30}}}
	}
	a := selectionApp(t, roots, busy, nil)
	h := newPluginRuntime(a)
	ev, err := h.Verify(t.Context(), pkg)
	if err != nil {
		t.Fatal(err)
	}
	// Nothing serves: these facts cannot hold the full set.
	if p, err := h.Prepare(t.Context(), ev); err != nil || p.HostBytes != smallSet.host {
		t.Fatalf("with nothing serving: %+v, %v", p, err)
	}
	profile := fullSet.profile("full-model")
	serving := &pluginhost.ActivePlugin{ID: componentSetsID, Version: "1.0.0", VariantID: "full", Accelerator: &profile}
	a.pluginMu.Lock()
	a.plugins = []*pluginhost.ActivePlugin{serving}
	a.activeMeta = map[string]activePkgMeta{componentSetsID: {pkg: "old.aiiospkg",
		owner: &pluginhost.Activation{ID: componentSetsID, Version: "1.0.0", Plugin: serving}}}
	a.pluginMu.Unlock()
	p, err := h.Prepare(t.Context(), ev)
	if err != nil {
		t.Fatal(err)
	}
	if p.HostBytes != fullSet.host {
		t.Fatalf("the update reserved %d: it stepped down from the set it replaces", p.HostBytes)
	}
}

// WHY A SET WAS PASSED OVER REACHES THE OPERATOR'S LOG. The sentence used to
// go to a logger the application never set.
func TestSelectionReasonsReachTheLog(t *testing.T) {
	logs := logsink.CaptureForTest(t)
	roots, pkg := componentSetsPackage(t, "1.0.0", fullSet, smallSet)
	noGPU := func() pluginfacility.Availability {
		return pluginfacility.Availability{HostKnown: true, HostTotal: 32 << 30, HostAvailable: 16 << 30}
	}
	h := newPluginRuntime(selectionApp(t, roots, noGPU, nil))
	ev, err := h.Verify(t.Context(), pkg)
	if err != nil {
		t.Fatal(err)
	}
	if p, err := h.Prepare(t.Context(), ev); err != nil || p.HostBytes != smallSet.host {
		t.Fatalf("%+v, %v", p, err)
	}
	for _, line := range logs.Lines() {
		if strings.Contains(line, "accelerator:cuda availability is unknown") && strings.Contains(line, "full") && strings.Contains(line, "small") {
			return
		}
	}
	t.Fatalf("no log line says which set was chosen and why full was passed over:\n%s", logs.String())
}

func TestPluginOptionsMeasureTheHostForSelection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Identity.LedgerPath = filepath.Join(t.TempDir(), "identity.ledger")
	a := &App{cfg: cfg}
	opts, err := a.buildPluginOptions(newAdmissionStore(t, t.TempDir()), tools.NewRegistry(t.TempDir(), nil, tools.Timeouts{}), nil)
	if err != nil {
		t.Fatal(err)
	}
	// Both sets fit any host that measures its memory at all; the first
	// also needs an accelerator no collector reports.
	roots, pkg := componentSetsPackage(t, "1.0.0", componentSet{host: 1 << 20, device: 1 << 20, required: []string{"aii-test-unmeasured"}}, componentSet{host: 1 << 20})
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	opts.Roots, opts.WorkerBinary = roots, exe // the supervised lane; nothing here executes it
	a.pluginOpts = opts
	h := newPluginRuntime(a)
	ev, err := h.Verify(t.Context(), pkg)
	if err != nil {
		t.Fatal(err)
	}
	p, err := h.Prepare(t.Context(), ev)
	if err != nil {
		t.Fatalf("selection was not handed this host's measurement: %v", err)
	}
	if p.HostBytes != 1<<20 {
		t.Fatalf("selected a set reserving %d", p.HostBytes)
	}
}

// THE OPERATOR'S DOORS RE-SELECT. Try again and an install are the operator
// asking afresh: the set kept for the plugin is released, and the next
// attempt selects as a first one would — through the application's own Retry
// door and catalog install, against its own plugin options.
func TestTryAgainAndAnInstallSelectAfresh(t *testing.T) {
	dir := t.TempDir()
	roots, pkg := componentSetsPackage(t, "1.0.0", fullSet, smallSet)
	raw, err := os.ReadFile(pkg)
	if err != nil {
		t.Fatal(err)
	}
	url, sha := servePackage(t, raw)
	catDir, rootPath, _ := signedCatalogDir(t, dir, plugincatalog.Index{Version: 1, Plugins: []plugincatalog.CatalogEntry{
		{ID: componentSetsID, Version: "1.0.0", Tier: "T3", Summary: "two component sets",
			Packages: []plugincatalog.CatalogPackage{{Platform: packagefmt.HostPlatform(), Arch: packagefmt.HostArch(), URL: url, SHA256: sha, Size: int64(len(raw))}}}}})
	cfg := &Config{Identity: IdentityConfig{LedgerPath: filepath.Join(dir, "identity.ledger")}, SourcePath: filepath.Join(dir, "config.json"),
		Plugins: PluginsConfig{PlatformRoot: rootPath, CatalogDir: catDir}}
	a := &App{cfg: cfg}
	opts, err := a.buildPluginOptions(newAdmissionStore(t, t.TempDir()), tools.NewRegistry(t.TempDir(), nil, tools.Timeouts{}), nil)
	if err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	room := roomy()
	opts.Roots, opts.WorkerBinary = roots, exe
	opts.ResourceFacts = func(context.Context) (pluginfacility.Availability, error) {
		mu.Lock()
		defer mu.Unlock()
		return room, nil
	}
	a.pluginOpts, a.pkgFetch = opts, loopbackFetcher()
	set := func(av pluginfacility.Availability) { mu.Lock(); room = av; mu.Unlock() }
	dip := roomy()
	dip.HostAvailable = 7 << 30
	h := newPluginRuntime(a)
	ev, err := h.Verify(t.Context(), pkg)
	if err != nil {
		t.Fatal(err)
	}
	attempt := func() int64 {
		t.Helper()
		p, err := h.Prepare(t.Context(), ev)
		if err != nil {
			t.Fatal(err)
		}
		actx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
		defer cancel()
		if err := h.Acquire(actx, p, nil); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("the material is not in flight: %v", err)
		}
		return p.HostBytes
	}
	if attempt() != fullSet.host {
		t.Fatal("the first attempt did not choose the full set")
	}
	set(dip)
	if attempt() != fullSet.host {
		t.Fatal("a dip swapped the kept set")
	}
	// Try again: the facility must know the plugin for its door to open.
	a.pluginFacility().Observe([]pluginfacility.Observed{{ID: ev.ID, Dir: filepath.Dir(pkg), Package: pkg, Hash: ev.PackageHash, ManifestHash: ev.ManifestHash}},
		pluginfacility.Policy{Revision: 1})
	if err := a.RetryPlugin(componentSetsID); err != nil {
		t.Fatal(err)
	}
	if got := attempt(); got != smallSet.host {
		t.Fatalf("after Try again the attempt reserved %d: the kept set was not released", got)
	}

	set(roomy())
	if attempt() != fullSet.host {
		t.Fatal("the set chosen in the dip was kept against the set the machine can run now")
	}
	set(dip)
	if attempt() != fullSet.host {
		t.Fatal("a dip swapped the kept set")
	}
	t.Chdir(dir)
	if err := a.InstallFromCatalog(t.Context(), componentSetsID); err != nil {
		t.Fatal(err)
	}
	if got := attempt(); got != smallSet.host {
		t.Fatalf("after an install the attempt reserved %d: the kept set was not released", got)
	}
}
