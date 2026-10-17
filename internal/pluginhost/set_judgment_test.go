//go:build (linux && !android) || (darwin && !ios) || windows

package pluginhost

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt/packagetest"
	"github.com/aiii-dot-id/aii-os/internal/pluginfacility"
)

// shapeSet is one component set of a test package: where it runs, what it
// reserves (no profile when profile is nil), what runtime it declares and
// which model it needs.
type shapeSet struct {
	id, platform, arch string
	profile            *AcceleratorProfile
	runtime            RuntimeDecl
	fixture            *runtimeFixture // the real tree behind runtime, when it is fetched
	model              ModelDecl
}

// shape is a signed package of component sets, and the bytes behind every
// URL it names.
type shape struct {
	id, pkg string
	version string // "" is 1.0.0
	roots   packagefmt.TrustRoots
	sets    []shapeSet
	bodies  map[string][]byte
}

func (s shape) versionOr() string {
	if s.version == "" {
		return "1.0.0"
	}
	return s.version
}

var signedShapes = struct {
	sync.Mutex
	built map[string]shape
	raw   map[string][]byte
}{built: map[string]shape{}, raw: map[string][]byte{}}

// shapeFor signs the named package once per test binary (signing costs
// seconds) and writes a copy for t.
func shapeFor(t *testing.T, name string, build func(t *testing.T) (shape, map[string]any)) shape {
	t.Helper()
	signedShapes.Lock()
	defer signedShapes.Unlock()
	s, ok := signedShapes.built[name]
	if !ok {
		c := &signedComponentSets
		c.Lock()
		if c.role == nil {
			c.roots, c.role = platformRootsForTest(t)
		}
		roots, role := c.roots, c.role
		c.Unlock()
		var extra map[string]any
		s, extra = build(t)
		s.roots = roots
		files := map[string][]byte{"interfaces/quarantine.probe.v1.schema.json": []byte(`{"interface":"quarantine.probe","v":1}`)}
		var variants []packagetest.VariantSpec
		var runtimes []RuntimeDecl
		var models []ModelDecl
		profiles := map[string]AcceleratorProfile{}
		for _, set := range s.sets {
			entry := "variants/" + set.id + "/plugin"
			files[entry] = []byte("#!carrier " + set.id + "\n")
			if set.fixture != nil {
				files[entry] = set.fixture.entry.Bytes
			}
			variants = append(variants, packagetest.VariantSpec{ID: set.id, Platform: set.platform, Arch: set.arch, Topology: packagefmt.HostTopology(),
				Runtime: "native_t3_component", Profile: "platform_reserved", Entrypoint: entry})
			set.runtime.VariantID = set.id
			runtimes = append(runtimes, set.runtime)
			if !slices.ContainsFunc(models, func(m ModelDecl) bool { return m.Name == set.model.Name }) {
				models = append(models, set.model)
			}
			if set.profile != nil {
				p := *set.profile
				p.OS, p.Arch, p.Models = set.platform, set.arch, []string{set.model.Name}
				profiles[set.id] = p
			}
		}
		files[RuntimesFile], _ = json.Marshal(map[string]any{"runtimes": runtimes})
		files[ModelsFile], _ = json.Marshal(models)
		if len(profiles) > 0 {
			files[AcceleratorFile], _ = json.Marshal(profiles)
		}
		manifest := packagetest.BuildManifestJSON(s.id, s.versionOr(), []packagetest.InterfaceSpec{{ID: "quarantine.probe", Version: 1, SchemaFile: "interfaces/quarantine.probe.v1.schema.json", Methods: []string{"ping"}}},
			variants, files, extra)
		spec := packagetest.PackageSpec{Root: s.id + "-" + s.versionOr(), Manifest: manifest, InstallFiles: files}
		if err := role.SignT3(&spec); err != nil {
			t.Fatal(err)
		}
		signedShapes.raw[name] = packagetest.Build(spec)
		signedShapes.built[name] = s
	}
	s.pkg = filepath.Join(t.TempDir(), s.id+"-"+s.versionOr()+".aiiospkg")
	if err := os.WriteFile(s.pkg, signedShapes.raw[name], 0o644); err != nil {
		t.Fatal(err)
	}
	return s
}

const gib = int64(1 << 30)

func hexOf(b []byte) string { sum := sha256.Sum256(b); return hex.EncodeToString(sum[:]) }

func declaredShape(t *testing.T) shape {
	return shapeFor(t, "declared", func(t *testing.T) (shape, map[string]any) {
		largest, depth := int64(600<<20), 6
		compactLargest := int64(200 << 20)
		large := preferenceProfile(6*gib, 9*gib, "cuda")
		compact := preferenceProfile(gib, 0)
		return shape{id: "org.example.sets", sets: []shapeSet{
			{id: "large", platform: packagefmt.HostPlatform(), arch: packagefmt.HostArch(), profile: &large,
				runtime: RuntimeDecl{URL: "https://cdn.example.org/large.tar.gz", SHA256: strings.Repeat("a", 64), InventorySHA256: strings.Repeat("b", 64),
					Size: 1591128917, InstalledBytes: 2498192347, Files: 2000, LargestFileBytes: &largest, Depth: &depth},
				model: ModelDecl{Name: "large-model", URL: "https://cdn.example.org/large-model", SHA256: strings.Repeat("c", 64), Size: 300_000_000}},
			{id: "compact", platform: packagefmt.HostPlatform(), arch: packagefmt.HostArch(), profile: &compact,
				runtime: RuntimeDecl{URL: "https://cdn.example.org/compact.tar.gz", SHA256: strings.Repeat("d", 64), InventorySHA256: strings.Repeat("e", 64),
					Size: 200_000_000, InstalledBytes: 500_000_000, Files: 900, LargestFileBytes: &compactLargest, Depth: &depth},
				model: ModelDecl{Name: "compact-model", URL: "https://cdn.example.org/compact-model", SHA256: strings.Repeat("f", 64), Size: 50_000_000}},
		}}, map[string]any{"aiios_min_version": packagefmt.RuntimeExtentMinHost, "default_variant": "large", "variant_preference": []string{"large", "compact"}}
	})
}

// gpuHost is a workstation with a 12 GiB CUDA device, of which free is free now.
func gpuHost(free int64) pluginfacility.Availability {
	return pluginfacility.Availability{HostKnown: true, HostTotal: 64 * gib, HostAvailable: 48 * gib,
		Devices: map[string]pluginfacility.DeviceAvailability{"cuda": {Known: true, Total: 12 * gib, Available: free}}}
}

// judgeOpts selects against facts, with free bytes on every volume.
func judgeOpts(s shape, facts pluginfacility.Availability, free int64) *Options {
	return &Options{Roots: s.roots, WorkerBinary: "unused-before-start", StoreDir: filepath.Join(os.TempDir(), "unused-store"),
		ResourceFacts: func(context.Context) (pluginfacility.Availability, error) { return facts, nil },
		FreeDisk:      func(string) (int64, error) { return free, nil }}
}

func judge(t *testing.T, s shape, opts *Options) (Selection, error) {
	t.Helper()
	res, err := Verify(s.pkg, opts)
	if err != nil {
		t.Fatal(err)
	}
	return SelectContext(t.Context(), s.pkg, res, opts)
}

func verdictOf(t *testing.T, sets []pluginfacility.SetVerdict, id string) pluginfacility.SetVerdict {
	t.Helper()
	for _, v := range sets {
		if v.Variant == id {
			return v
		}
	}
	t.Fatalf("no verdict for %s in %+v", id, sets)
	return pluginfacility.SetVerdict{}
}

func reasonsText(v pluginfacility.SetVerdict) string {
	var b strings.Builder
	for _, r := range v.Reasons {
		b.WriteString(r.Words + " => " + r.Remedy + "\n")
	}
	return b.String()
}

func TestTheSetTheMachineCanRunIsChosenWhateverItsSize(t *testing.T) {
	s := declaredShape(t)
	sel, err := judge(t, s, judgeOpts(s, gpuHost(11*gib), 500*gib))
	if err != nil {
		t.Fatal(err)
	}
	if sel.VariantID != "large" {
		t.Fatalf("selected %s; passed over %v", sel.VariantID, sel.ResourceRefusals)
	}
	if len(sel.ResourceRefusals) != 0 {
		t.Fatalf("the large set fits and nothing is passed over for this machine: %v", sel.ResourceRefusals)
	}
	large, compact := verdictOf(t, sel.Sets, "large"), verdictOf(t, sel.Sets, "compact")
	if !large.Chosen || !strings.Contains(large.Why, "first in the package's order") || compact.Chosen {
		t.Fatalf("chosen and why: %+v / %+v", large, compact)
	}
	for _, want := range []string{"4.09 GB more on disk", "500.00 GB free"} {
		if !strings.Contains(large.Storage, want) {
			t.Fatalf("(a) storage says %q, want %q", large.Storage, want)
		}
	}
	if !strings.Contains(large.Bandwidth, "1.76 GB to download from cdn.example.org") || !strings.Contains(large.Bandwidth, "not yet measured") {
		t.Fatalf("(b) bandwidth says %q", large.Bandwidth)
	}
	if !strings.Contains(large.Resources, "9.00 GB of the cuda device") {
		t.Fatalf("(c) resources say %q", large.Resources)
	}
	if !strings.Contains(reasonsText(compact), "the package prefers large") {
		t.Fatalf("why the compact set was not chosen: %s", reasonsText(compact))
	}
	if sel.Sets[0].Variant != "large" || len(sel.Sets) != 2 {
		t.Fatalf("the sets in the package's order: %+v", sel.Sets)
	}
}

// (b) The same package where the device cannot hold the large set: for good
// (8 GiB in all) or for now (other processes hold the rest).
func TestASetTheMachineCannotHoldIsPassedOverInWords(t *testing.T) {
	s := declaredShape(t)
	small := gpuHost(8 * gib)
	small.Devices["cuda"] = pluginfacility.DeviceAvailability{Known: true, Total: 8 * gib, Available: 8 * gib}
	sel, err := judge(t, s, judgeOpts(s, small, 500*gib))
	if err != nil || sel.VariantID != "compact" {
		t.Fatalf("an 8 GiB device: %s, %v", sel.VariantID, err)
	}
	why := reasonsText(verdictOf(t, sel.Sets, "large"))
	if !strings.Contains(why, "needs 9.00 GB of the cuda device's memory; it has 8.00 GB in all") {
		t.Fatalf("a fixed reason in words: %s", why)
	}
	busy := gpuHost(41 * gib / 10)
	sel, err = judge(t, s, judgeOpts(s, busy, 500*gib))
	if err != nil || sel.VariantID != "compact" {
		t.Fatalf("a busy device: %s, %v", sel.VariantID, err)
	}
	large := verdictOf(t, sel.Sets, "large")
	why = reasonsText(large)
	if !strings.Contains(why, "needs 9.00 GB of the cuda device's 12.00 GB; 4.10 GB is free now, the rest held by other processes") ||
		!strings.Contains(why, "chosen at a later activation once that memory is free") {
		t.Fatalf("a momentary reason in words, with its remedy: %s", why)
	}
	if !strings.Contains(large.Resources, "held by other processes") {
		t.Fatalf("(c) resources say %q", large.Resources)
	}
}

// (c) Room for the large set on the device, not on the disk.
func TestTooLittleFreeDiskPassesASetOverWithTheSpaceToFree(t *testing.T) {
	s := declaredShape(t)
	sel, err := judge(t, s, judgeOpts(s, gpuHost(11*gib), 3*gib))
	if err != nil || sel.VariantID != "compact" {
		t.Fatalf("3 GiB free: %s, %v", sel.VariantID, err)
	}
	large := verdictOf(t, sel.Sets, "large")
	why := reasonsText(large)
	for _, want := range []string{"needs 4.09 GB more on disk at its peak", "3.00 GB is free", "keeps 2.00 GB of that free", "=> free 3.09 GB on that volume"} {
		if !strings.Contains(why, want) {
			t.Fatalf("the disk words want %q: %s", want, why)
		}
	}
	compact := verdictOf(t, sel.Sets, "compact")
	if !compact.Chosen || !strings.Contains(compact.Storage, "715.3 MB more on disk") {
		t.Fatalf("the compact set fits: %+v", compact)
	}
	// An unmeasured volume is never room: nothing that still has bytes to
	// write is chosen, and the refusal carries each set's judgment.
	opts := judgeOpts(s, gpuHost(11*gib), 0)
	opts.FreeDisk = func(string) (int64, error) { return 0, errors.New("statfs: input/output error") }
	_, err = judge(t, s, opts)
	var refused *VariantSelectionError
	if !errors.As(err, &refused) {
		t.Fatalf("an unmeasured disk admitted a set: %v", err)
	}
	judged := refused.Judgment()
	if judged == nil || len(judged.Sets) != 2 || judged.Variant != "" ||
		!strings.Contains(reasonsText(verdictOf(t, judged.Sets, "compact")), "could not be measured: statfs: input/output error") {
		t.Fatalf("the refusal's judgment: %+v", judged)
	}
}

// (d) A ceiling the operator set is his limit, named with the value that
// admits the set.
func TestAnOperatorCeilingNamesItsKeyAndTheValueThatAdmits(t *testing.T) {
	s := declaredShape(t)
	opts := judgeOpts(s, gpuHost(11*gib), 500*gib)
	opts.RuntimeLimits.MaxCompressedBytes = 512 << 20
	sel, err := judge(t, s, opts)
	if err != nil || sel.VariantID != "compact" {
		t.Fatalf("an operator's 512 MiB archive ceiling: %s, %v", sel.VariantID, err)
	}
	why := reasonsText(verdictOf(t, sel.Sets, "large"))
	if !strings.Contains(why, "its runtime archive is 1.48 GB; the operator's ceiling plugins.runtime.max_compressed_bytes is 512.0 MB") ||
		!strings.Contains(why, "=> set plugins.runtime.max_compressed_bytes to 1591128917 or more, or clear it so that free disk decides") {
		t.Fatalf("the ceiling's words and remedy: %s", why)
	}
	if got := strings.Join(sel.ResourceRefusals[0].Missing, "; "); !strings.Contains(got, "runtime compressed bytes declares 1591128917, operator ceiling 536870912 (plugins.runtime.max_compressed_bytes)") {
		t.Fatalf("the log keeps its machine form: %s", got)
	}
	// The structural ceilings keep their defaults, and say so the same way.
	opts = judgeOpts(s, gpuHost(11*gib), 500*gib)
	opts.RuntimeLimits.MaxFiles = 1000
	sel, err = judge(t, s, opts)
	if err != nil || sel.VariantID != "compact" ||
		!strings.Contains(reasonsText(verdictOf(t, sel.Sets, "large")), "=> set plugins.runtime.max_files to 2000 or more") {
		t.Fatalf("a files ceiling: %s, %v, %+v", sel.VariantID, err, sel.Sets)
	}
}

// realShape is a package whose sets are real trees: the large one carries
// 64 KiB more than the compact one.
func realShape(t *testing.T) shape {
	return shapeFor(t, "real", func(t *testing.T) (shape, map[string]any) {
		large := newRuntimeFixtureOf(t, map[string][]byte{"bin/engine": bytes.Repeat([]byte("L"), 4096), "lib/weights.bin": bytes.Repeat([]byte("w"), 64<<10)})
		compact := newRuntimeFixtureOf(t, map[string][]byte{"bin/engine": bytes.Repeat([]byte("C"), 4096)})
		large.decl.URL, compact.decl.URL = "https://cdn.example.org/large.tar.gz", "https://cdn.example.org/compact.tar.gz"
		// Each set's model its own bytes: in the plugin store one digest is
		// one blob, and these tests count each set's own download.
		largeWeights, compactWeights := []byte("the large set's verified model"), []byte("the compact set's verified model")
		lp, cp := preferenceProfile(6*gib, 6*gib, "cuda"), preferenceProfile(gib, 0)
		s := shape{id: "org.example.rejudged", bodies: map[string][]byte{large.decl.URL: large.archive, compact.decl.URL: compact.archive,
			"https://cdn.example.org/large-model": largeWeights, "https://cdn.example.org/compact-model": compactWeights}}
		s.sets = []shapeSet{
			{id: "large", platform: packagefmt.HostPlatform(), arch: packagefmt.HostArch(), profile: &lp, runtime: large.decl, fixture: large,
				model: ModelDecl{Name: "large-model", URL: "https://cdn.example.org/large-model", SHA256: hexOf(largeWeights), Size: int64(len(largeWeights))}},
			{id: "compact", platform: packagefmt.HostPlatform(), arch: packagefmt.HostArch(), profile: &cp, runtime: compact.decl, fixture: compact,
				model: ModelDecl{Name: "compact-model", URL: "https://cdn.example.org/compact-model", SHA256: hexOf(compactWeights), Size: int64(len(compactWeights))}},
		}
		return s, map[string]any{"aiios_min_version": packagefmt.VariantPreferenceMinHost, "default_variant": "large", "variant_preference": []string{"large", "compact"}}
	})
}

// fetchRecord serves a shape's bodies and records every URL asked for.
type fetchRecord struct {
	s     shape
	mu    sync.Mutex
	asked []string
}

func (f *fetchRecord) fetch(_ context.Context, url string, offset int64, w io.Writer) (int64, error) {
	f.mu.Lock()
	f.asked = append(f.asked, url)
	f.mu.Unlock()
	body, ok := f.s.bodies[url]
	if !ok {
		return 0, errors.New("not a declared URL")
	}
	n, err := w.Write(body[offset:])
	return int64(n), err
}

func (f *fetchRecord) urls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.asked...)
}

// acquiredBy stages and acquires through an attached Acquirer until the
// material is on disk, returning the staged package.
func acquiredBy(t *testing.T, s shape, opts *Options, ready chan string) *Staged {
	t.Helper()
	res, err := Verify(s.pkg, opts)
	if err != nil {
		t.Fatal(err)
	}
	staged, err := StagePreparedContext(t.Context(), s.pkg, res, opts)
	if err != nil {
		t.Fatal(err)
	}
	if err := staged.Acquire(t.Context()); err != nil {
		var acquiring *AcquiringError
		if !errors.As(err, &acquiring) {
			t.Fatal(err)
		}
		select {
		case <-ready:
		case <-time.After(10 * time.Second):
			t.Fatal("the material never arrived")
		}
		if err := staged.Acquire(t.Context()); err != nil {
			t.Fatalf("the material is on disk: %v", err)
		}
	}
	return staged
}

// servingOf is what serves once staged's set does.
func servingOf(staged *Staged) *ServingSet {
	return &ServingSet{PackageHash: staged.res.PackageHash, ManifestHash: staged.res.ManifestHash, Variant: staged.Selection().VariantID}
}

// (e) EVERY IDENTITY IS JUDGED AGAIN. An identity whose last activation chose
// the compact set — under a ceiling its configuration held, or while other
// processes held the device — chooses the large set at its next activation
// once the machine can run it, with nothing changed by hand, and fetches
// only the large set's material. Since the swap (set_swap.go), that next
// activation serves the installed compact set at once and names the large
// one as the set it serves for; the candidate beside it chooses the large
// set and fetches its material.
func TestAnIdentityThatChoseTheCompactSetChoosesTheLargeOneAtItsNextActivation(t *testing.T) {
	s := realShape(t)
	w := openStore(t)
	room := gpuHost(11 * gib)
	var mu sync.Mutex
	var serving *ServingSet
	opts := func(acq *Acquirer, limits packagefmt.TreeLimits) *Options {
		return &Options{Roots: s.roots, WorkerBinary: "unused-before-start", StoreDir: w.Dir,
			RuntimeLimits: limits, Acquirer: acq, FreeDisk: func(string) (int64, error) { return 500 * gib, nil }, Serving: serving,
			ResourceFacts: func(context.Context) (pluginfacility.Availability, error) {
				mu.Lock()
				defer mu.Unlock()
				return room, nil
			}}
	}
	large, compact := s.sets[0].fixture, s.sets[1].fixture

	// The last activation under the old loader: the ceiling its file held
	// admitted only the compact set.
	first := &fetchRecord{s: s}
	acq, ready := attachedRecorder(t, first, w)
	staged := acquiredBy(t, s, opts(acq, packagefmt.TreeLimits{MaxInstalledBytes: compact.decl.InstalledBytes}), ready)
	if staged.Selection().VariantID != "compact" {
		t.Fatalf("the old ceiling chose %s", staged.Selection().VariantID)
	}

	next := &fetchRecord{s: s}
	acq, ready = attachedRecorder(t, next, w)
	staged = acquiredBy(t, s, opts(acq, packagefmt.TreeLimits{}), ready)
	if sel := staged.Selection(); sel.VariantID != "compact" || sel.Better != "large" || len(next.urls()) != 0 {
		t.Fatalf("the next activation chose %s for %q, fetching %v; passed over %v", sel.VariantID, sel.Better, next.urls(), sel.ResourceRefusals)
	}
	serving = servingOf(staged)
	staged = acquiredBy(t, s, opts(acq, packagefmt.TreeLimits{}), ready)
	serving = nil
	if staged.Selection().VariantID != "large" {
		t.Fatalf("the candidate chose %s; passed over %v", staged.Selection().VariantID, staged.Selection().ResourceRefusals)
	}
	if got := next.urls(); !slices.Equal(got, []string{"https://cdn.example.org/large-model", large.decl.URL}) {
		t.Fatalf("the move fetched %v; only the large set's model and runtime", got)
	}

	// Within one host process: the compact set chosen while other processes
	// held the device is not kept against the large one once the device is
	// free. The kept set still waits out a dip of its own.
	w.Close()
	w = openStore(t)
	mu.Lock()
	room = gpuHost(2 * gib)
	mu.Unlock()
	same := &fetchRecord{s: s}
	acq, ready = attachedRecorder(t, same, w)
	if got := acquiredBy(t, s, opts(acq, packagefmt.TreeLimits{}), ready).Selection().VariantID; got != "compact" {
		t.Fatalf("a busy device chose %s", got)
	}
	mu.Lock()
	room = gpuHost(11 * gib)
	mu.Unlock()
	staged = acquiredBy(t, s, opts(acq, packagefmt.TreeLimits{}), ready)
	if sel := staged.Selection(); sel.VariantID != "compact" || sel.Better != "large" {
		t.Fatalf("the next activation in the same process chose %s for %q; the device is free and the compact set is on disk", sel.VariantID, sel.Better)
	}
	serving = servingOf(staged)
	staged = acquiredBy(t, s, opts(acq, packagefmt.TreeLimits{}), ready)
	serving = nil
	if sel := staged.Selection(); sel.VariantID != "large" || sel.Kept {
		t.Fatalf("the candidate in the same process chose %s (kept=%v); the device is free", sel.VariantID, sel.Kept)
	}
	mu.Lock()
	room = gpuHost(2 * gib)
	mu.Unlock()
	if sel := acquiredBy(t, s, opts(acq, packagefmt.TreeLimits{}), ready).Selection(); sel.VariantID != "large" || !sel.Kept {
		t.Fatalf("a dip swapped the set on disk: %s (kept=%v)", sel.VariantID, sel.Kept)
	}
	if got := same.urls(); len(got) != 4 || slices.Contains(got[2:], compact.decl.URL) {
		t.Fatalf("fetches %v: the compact set's material once, then the large set's", got)
	}
}

func TestATreeLargerThanTheOldDefaultsIsExtractedWithNoCeilingSet(t *testing.T) {
	fx := newRuntimeFixtureOf(t, map[string][]byte{"bin/engine": bytes.Repeat([]byte("e"), 8192), "lib/data.bin": bytes.Repeat([]byte("d"), 32<<10)})
	old := packagefmt.DefaultTreeLimits
	t.Cleanup(func() { packagefmt.DefaultTreeLimits = old })
	packagefmt.DefaultTreeLimits.MaxCompressedBytes = fx.decl.Size - 1
	packagefmt.DefaultTreeLimits.MaxInstalledBytes = fx.decl.InstalledBytes - 1
	packagefmt.DefaultTreeLimits.MaxFileBytes = 1024
	entry := entrypointSpec{Name: "engine-carrier", Bytes: []byte("#!carrier\n"), Digest: "sha256:" + hexOf([]byte("#!carrier\n"))}
	w := openStore(t)
	root, err := storeRoot(t, w, &fx.decl, entry, fx.fetch, packagefmt.TreeLimits{})
	if err != nil {
		t.Fatalf("a tree past the old defaults, no ceiling set: %v", err)
	}
	// And the root verifies again under the same effective limits.
	if err := verifyRootIn(t, w, root, &fx.decl, entry, packagefmt.TreeLimits{}); err != nil {
		t.Fatalf("the published root does not verify again: %v", err)
	}
	// A ceiling the operator set still holds, by its key.
	_, err = storeRoot(t, openStore(t), &fx.decl, entry, fx.fetch, packagefmt.TreeLimits{MaxInstalledBytes: fx.decl.InstalledBytes - 1})
	if err == nil || !strings.Contains(err.Error(), "plugins.runtime.max_installed_bytes") {
		t.Fatalf("the operator's ceiling: %v", err)
	}
}

// The one owner of the limits selection and extraction both read: the
// operator's where set, else the declaration's own numbers for bytes and
// the structural defaults for files and depth.
func TestTheEffectiveLimitsHaveOneOwner(t *testing.T) {
	largest, depth := int64(600<<20), 6
	d := RuntimeDecl{Size: 1591128917, InstalledBytes: 2498192347, Files: 2000, LargestFileBytes: &largest, Depth: &depth}
	// One file is bounded by the whole tree; the declared largest is held
	// to the inventory exactly, as the package's claim (extentMismatch).
	got := runtimeLimits(d, packagefmt.TreeLimits{})
	if got.MaxCompressedBytes != d.Size || got.MaxInstalledBytes != d.InstalledBytes || got.MaxFileBytes != d.InstalledBytes ||
		got.MaxFiles != packagefmt.DefaultTreeLimits.MaxFiles || got.MaxDepth != packagefmt.DefaultTreeLimits.MaxDepth || got.MaxInventoryBytes != packagefmt.DefaultTreeLimits.MaxInventoryBytes {
		t.Fatalf("no ceiling set: %+v", got)
	}
	set := packagefmt.TreeLimits{MaxCompressedBytes: 1, MaxInstalledBytes: 2, MaxFileBytes: 3, MaxFiles: 4, MaxDepth: 5, MaxInventoryBytes: 6}
	if got := runtimeLimits(d, set); got != set {
		t.Fatalf("the operator's ceilings are his: %+v", got)
	}
}

// (b) BANDWIDTH IS MEASURED, SHOWN, AND NEVER A VETO. The host's own fetches
// are timed per source host; a set's verdict says what it still has to
// download and, once the link is measured, about how long that takes.
func TestTheDownloadIsMeasuredAndShownNeverAVeto(t *testing.T) {
	s := declaredShape(t)
	opts := judgeOpts(s, gpuHost(11*gib), 500*gib)
	slow := func(_ context.Context, _ string, _ int64, w io.Writer) (int64, error) {
		time.Sleep(50 * time.Millisecond)
		n, err := w.Write(make([]byte, 2<<20))
		return int64(n), err
	}
	opts.Acquirer = NewAcquirer(AcquirerConfig{Fetcher: slow, Logf: func(string, ...any) {}})
	sel, err := judge(t, s, opts)
	if err != nil || !strings.Contains(verdictOf(t, sel.Sets, "large").Bandwidth, "the speed of this link is not yet measured") {
		t.Fatalf("before any fetch: %v %+v", err, sel.Sets)
	}
	if _, err := opts.Acquirer.cfg.Fetcher(t.Context(), "https://cdn.example.org/warm", 0, io.Discard); err != nil {
		t.Fatal(err)
	}
	rate, ok := opts.Acquirer.linkRate("cdn.example.org")
	if !ok || rate <= 0 || rate > float64(2<<20)/0.05 {
		t.Fatalf("the measured rate: %v, %v", rate, ok)
	}
	sel, err = judge(t, s, opts)
	if err != nil {
		t.Fatal(err)
	}
	large := verdictOf(t, sel.Sets, "large")
	if !regexp.MustCompile(`1\.76 GB to download from cdn\.example\.org; about [0-9.]+ (seconds|minutes|hours) at the [0-9.]+ MB/s this host measured from there`).MatchString(large.Bandwidth) {
		t.Fatalf("(b) with a measured link: %q", large.Bandwidth)
	}
	if !large.Chosen {
		t.Fatalf("a slow link vetoed the set the machine can run: %+v", sel.Sets)
	}
	// What is on disk is not downloaded again.
	if _, ok := opts.Acquirer.linkRate("other.example.net"); ok {
		t.Fatal("a host never fetched from has a rate")
	}
}

// ONE PATH FOR EVERY PLUGIN: a package with one CPU set per platform and no
// order of sets is judged the same way — its storage and bandwidth, the
// operator's ceilings — and refused with the judgment when its one set
// cannot be installed.
func TestOneSetPerPlatformIsJudgedOnTheSamePath(t *testing.T) {
	s := shapeFor(t, "per-platform", func(t *testing.T) (shape, map[string]any) {
		coords := [][2]string{{"linux", "x86_64"}, {"macos", "arm64"}, {"windows", "x86_64"}}
		if !slices.Contains(coords, [2]string{packagefmt.HostPlatform(), packagefmt.HostArch()}) {
			coords = append(coords, [2]string{packagefmt.HostPlatform(), packagefmt.HostArch()})
		}
		var sets []shapeSet
		for _, c := range coords {
			id := c[0] + "-" + c[1] + "-cpu"
			sets = append(sets, shapeSet{id: id, platform: c[0], arch: c[1],
				runtime: RuntimeDecl{URL: "https://cdn.example.org/" + id + ".tar.gz", SHA256: strings.Repeat("a", 64), InventorySHA256: strings.Repeat("b", 64),
					Size: 300_000_000, InstalledBytes: 900_000_000, Files: 40},
				model: ModelDecl{Name: "basis", URL: "https://cdn.example.org/basis", SHA256: strings.Repeat("c", 64), Size: 120_000_000}})
		}
		return shape{id: "org.example.perplatform", sets: sets}, map[string]any{"aiios_min_version": packagefmt.VariantPreferenceMinHost}
	})
	host := packagefmt.HostPlatform() + "-" + packagefmt.HostArch() + "-cpu"
	opts := judgeOpts(s, gpuHost(11*gib), 500*gib)
	sel, err := judge(t, s, opts)
	if err != nil || sel.VariantID != host || len(sel.Sets) != 1 {
		t.Fatalf("one set per platform: %s, %v, %+v", sel.VariantID, err, sel.Sets)
	}
	v := sel.Sets[0]
	if !v.Chosen || !strings.Contains(v.Why, "the one component set") || !strings.Contains(v.Storage, "1.23 GB more on disk") ||
		!strings.Contains(v.Bandwidth, "400.5 MB to download") || !strings.Contains(v.Resources, "admission") {
		t.Fatalf("the verdict: %+v", v)
	}
	// Its runtime past the operator's ceiling is refused at selection, by key.
	opts.RuntimeLimits.MaxInstalledBytes = 512 << 20
	_, err = judge(t, s, opts)
	var refused *VariantSelectionError
	if !errors.As(err, &refused) || !strings.Contains(reasonsText(refused.Judgment().Sets[0]), "set plugins.runtime.max_installed_bytes to 900000000 or more") {
		t.Fatalf("over the operator's ceiling: %v", err)
	}
	// And with too little disk, with the space to free.
	_, err = judge(t, s, judgeOpts(s, gpuHost(11*gib), 2*gib+100<<20))
	if !errors.As(err, &refused) || !strings.Contains(reasonsText(refused.Judgment().Sets[0]), "=> free 1.13 GB on that volume") {
		t.Fatalf("too little disk: %v", err)
	}
}

// A plugin with no native set — a WebAssembly module — has nothing to judge:
// nothing is measured, nothing is passed over, and the card shows nothing.
func TestAPluginWithNoNativeSetHasNothingJudged(t *testing.T) {
	pkg := writePkg(t, pkgSpec("org.example.module", "1.0.0", []string{"ping"}, fixtureWasm(t, "caller.wasm")))
	res, err := Verify(pkg, nil)
	if err != nil {
		t.Fatal(err)
	}
	sel, err := SelectContext(t.Context(), pkg, res, &Options{FreeDisk: func(string) (int64, error) {
		t.Fatal("a module with no native set measured the disk")
		return 0, nil
	}})
	if err != nil || len(sel.Sets) != 0 || len(sel.ResourceRefusals) != 0 {
		t.Fatalf("a module: %+v, %v", sel, err)
	}
}

// The platform's own measurement answers on this host, and a directory not
// made yet is measured where it will be made.
func TestFreeDiskIsMeasuredWhereTheMaterialGoes(t *testing.T) {
	free, err := freeDisk(filepath.Join(t.TempDir(), "not", "made", "yet"))
	if err != nil || free <= 0 {
		t.Fatalf("free disk on this host: %d, %v", free, err)
	}
}

func TestTheSetJudgmentNamesNoPlugin(t *testing.T) {
	banned := regexp.MustCompile(`(?i)voice|speech|\bmeaning\b|github|hugging ?face|"full"|"small"`)
	for _, f := range []string{"set_judgment.go", "set_swap.go", "free_disk.go", "free_disk_unix.go", "free_disk_windows.go", "free_disk_other.go", "acquire_rate.go",
		"download_rule.go", "acquire.go", "needs.go",

		"validation.go", "../packagefmt/validation.go", "../pluginfacility/checks.go",

		"session_judge.go"} {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		raw = bytes.ReplaceAll(raw, []byte("github.com/aiii-dot-id/aii-os/"), nil)
		// The repository's own evaluator of transcripts, whose normaliser
		// a session check's phrases are read with, is a host package and
		// names no plugin.
		raw = bytes.ReplaceAll(raw, []byte("speecheval"), nil)
		if m := banned.Find(raw); m != nil {
			t.Errorf("%s names %q", f, m)
		}
	}
}
