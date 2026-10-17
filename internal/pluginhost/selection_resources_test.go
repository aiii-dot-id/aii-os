//go:build (linux && !android) || (darwin && !ios) || windows

package pluginhost

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt/packagetest"
	"github.com/aiii-dot-id/aii-os/internal/pluginfacility"
)

// The declared compatibility floor must work in an ordinary host build, not
// just in fixtures that override the host version.
// guard:tree
func TestAuthoredHostSupportsVariantPreference(t *testing.T) {
	manifest := &packagefmt.Manifest{ID: "org.example.componentsets", Version: "1.0.0", AiiosMinVersion: packagefmt.VariantPreferenceMinHost}
	if err := checkHostWindow(manifest, hostVersionFor(nil)); err != nil {
		t.Fatalf("compiled host cannot use its own component-selection contract: %v", err)
	}
}

func TestResourceAwareSelection(t *testing.T) {
	const gib = int64(1 << 30)
	full := preferenceProfile(8*gib, 6*gib, "cuda")
	small := preferenceProfile(gib, 0)
	good := pluginfacility.Availability{HostKnown: true, HostTotal: 32 * gib, HostAvailable: 16 * gib, Devices: map[string]pluginfacility.DeviceAvailability{"cuda": {Known: true, Total: 8 * gib, Available: 8 * gib}}}
	for _, tc := range []struct {
		name   string
		facts  pluginfacility.Availability
		policy pluginfacility.AdmissionPolicy
		order  []string
		want   string
	}{
		{"full-fits", good, pluginfacility.AdmissionPolicy{}, nil, "full"},
		{"small-first", good, pluginfacility.AdmissionPolicy{}, []string{"small", "full"}, "small"},
		{"host-too-small", pluginfacility.Availability{HostKnown: true, HostTotal: 16 * gib, HostAvailable: 4 * gib, Devices: good.Devices}, pluginfacility.AdmissionPolicy{}, nil, "small"},
		{"gpu-unknown", pluginfacility.Availability{HostKnown: true, HostTotal: 32 * gib, HostAvailable: 16 * gib}, pluginfacility.AdmissionPolicy{}, nil, "small"},
		{"gpu-busy", pluginfacility.Availability{HostKnown: true, HostTotal: 32 * gib, HostAvailable: 16 * gib, Devices: map[string]pluginfacility.DeviceAvailability{"cuda": {Known: true, Total: 24 * gib, Available: 2 * gib}}}, pluginfacility.AdmissionPolicy{}, nil, "small"},
		{"budget", good, pluginfacility.AdmissionPolicy{BudgetBytes: 4 * gib}, nil, "small"},
		{"reserve", good, pluginfacility.AdmissionPolicy{ReserveBytes: 12 * gib}, nil, "small"},
		{"unknown-host", pluginfacility.Availability{}, pluginfacility.AdmissionPolicy{BudgetBytes: 16 * gib}, nil, ""},
		{"exhausted", pluginfacility.Availability{HostKnown: true, HostTotal: 32 * gib, HostAvailable: 0}, pluginfacility.AdmissionPolicy{}, nil, ""},
		{"invalid-measurement", pluginfacility.Availability{HostKnown: true, HostTotal: gib, HostAvailable: 2 * gib}, pluginfacility.AdmissionPolicy{}, nil, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := selResult(packagefmt.TierT3, "full", selVariant("full", "linux", "x86_64", "native_t3_component", "platform_reserved"), selVariant("small", "linux", "x86_64", "native_t3_component", "platform_reserved"))
			res.Manifest.VariantPreference = tc.order
			if tc.order == nil {
				res.Manifest.VariantPreference = []string{"full", "small"}
			}
			h := selHost(t, true)
			h.resources = &selectionResources{profiles: map[string]AcceleratorProfile{"full": full, "small": small}, available: tc.facts, policy: tc.policy}
			v, err := selectVariant(res, h)
			if tc.want == "" {
				if err == nil || pluginfacility.NewRefusal("org.example.engine", "1.0.0", 1, pluginfacility.StageMaterial, err).Class != pluginfacility.ClassTransient {
					t.Fatalf("capacity failure not transient: %v", err)
				}
				return
			}
			if err != nil || v.VariantID != tc.want {
				t.Fatalf("selected=%v err=%v want=%s", v, err, tc.want)
			}
		})
	}
	// Separate Metal and host figures describe the SAME pool, not two charges.
	uma := preferenceProfile(8*gib, 0, "metal")
	a := pluginfacility.Availability{HostKnown: true, HostTotal: 16 * gib, HostAvailable: 8 * gib, Devices: map[string]pluginfacility.DeviceAvailability{"metal": {Known: true, Total: 16 * gib, Available: 8 * gib, Unified: true}}}
	if missing := resourceRefusals(uma, a, pluginfacility.AdmissionPolicy{}); len(missing) != 0 {
		t.Fatal(missing)
	}
	n := gib
	uma.DeviceMemoryBytes = &n
	if missing := resourceRefusals(uma, a, pluginfacility.AdmissionPolicy{}); len(missing) == 0 {
		t.Fatal("unified memory double-accounting admitted")
	}
}

func TestRequiredAcceleratorsSharedVectors(t *testing.T) {
	raw, err := os.ReadFile("testdata/variant_selection.json")
	if err != nil {
		t.Fatal(err)
	}
	var vectors struct {
		Accelerators []struct {
			Name     string
			Required []string
			OK       bool
		}
	}
	if err := json.Unmarshal(raw, &vectors); err != nil {
		t.Fatal(err)
	}
	if len(vectors.Accelerators) < 8 {
		t.Fatal("thin vectors")
	}
	for _, v := range vectors.Accelerators {
		t.Run(v.Name, func(t *testing.T) {
			p := preferenceProfile(1, 0, v.Required...)
			b, _ := json.Marshal(map[string]AcceleratorProfile{"full": p})
			_, err := ParseAccelerators(b, []string{"full"})
			if (err == nil) != v.OK {
				t.Fatalf("accepted=%v want=%v: %v", err == nil, v.OK, err)
			}
		})
	}
}

// The real package path, not only a selector fixture: selected weights AND
// selected runtime materialise; rejected URLs are never fetched.
func TestPreferenceChoosesBeforeAcquiringAnyMaterial(t *testing.T) {
	for _, constraint := range []string{"memory", "disk", "runtime-compressed", "runtime-installed", "runtime-files", "runtime-largest-file", "runtime-depth"} {
		t.Run(constraint, func(t *testing.T) { provePreferenceBeforeMaterial(t, constraint) })
	}
}

func provePreferenceBeforeMaterial(t *testing.T, constraint string) {
	const id = "org.example.componentsets"
	roots, role := platformRootsForTest(t)
	fullFX, smallFX := newRuntimeFixtureWith(t, "full"), newRuntimeFixtureWith(t, "small")
	fullFX.decl.VariantID, smallFX.decl.VariantID = "full", "small"
	fullFX.decl.URL, smallFX.decl.URL = "https://example.com/full-runtime", "https://example.com/small-runtime"

	operator := packagefmt.TreeLimits{}
	switch constraint {
	case "disk":
		fullFX.decl.InstalledBytes = 4 << 30
	case "runtime-compressed":
		operator.MaxCompressedBytes = 512 << 20
		fullFX.decl.Size = operator.MaxCompressedBytes + 1
	case "runtime-installed":
		operator.MaxInstalledBytes = 1 << 30
		fullFX.decl.InstalledBytes = operator.MaxInstalledBytes + 1
	case "runtime-files":
		fullFX.decl.Files = packagefmt.DefaultTreeLimits.MaxFiles + 1
	case "runtime-largest-file", "runtime-depth":
		// The beta.7 Linux Full shape at test scale: within the totals the
		// operator raised, over a per-file or depth ceiling only its extent
		// can show before the download.
		operator.MaxFileBytes = 512 << 20
		largest, depth := operator.MaxFileBytes+1, 4
		if constraint == "runtime-depth" {
			operator.MaxFileBytes = 0
			largest, depth = 100<<20, packagefmt.DefaultTreeLimits.MaxDepth+1
		}
		fullFX.decl.InstalledBytes, fullFX.decl.Files = 600<<20, 10
		fullFX.decl.LargestFileBytes, fullFX.decl.Depth = &largest, &depth
	}
	extent := constraint == "runtime-largest-file" || constraint == "runtime-depth"
	floor := packagefmt.VariantPreferenceMinHost
	if extent {
		floor = packagefmt.RuntimeExtentMinHost
	}
	files := map[string][]byte{"interfaces/quarantine.probe.v1.schema.json": []byte(`{"interface":"quarantine.probe","v":1}`), "variants/full/plugin": fullFX.entry.Bytes, "variants/small/plugin": smallFX.entry.Bytes}
	weights := []byte("one verified model")
	hash := sha256.Sum256(weights)
	models := []ModelDecl{{Name: "full-model", URL: "https://example.com/full-model", SHA256: hex.EncodeToString(hash[:]), Size: int64(len(weights))}, {Name: "small-model", URL: "https://example.com/small-model", SHA256: hex.EncodeToString(hash[:]), Size: int64(len(weights))}}
	files[ModelsFile], _ = json.Marshal(models)
	profiles := map[string]AcceleratorProfile{"full": preferenceProfile(8<<30, 6<<30, "cuda"), "small": preferenceProfile(1<<30, 0)}
	for key, p := range profiles {
		p.OS, p.Arch = packagefmt.HostPlatform(), packagefmt.HostArch()
		p.Models = []string{key + "-model"}
		profiles[key] = p
	}
	files[AcceleratorFile], _ = json.Marshal(profiles)
	files[RuntimesFile], _ = json.Marshal(map[string]any{"runtimes": []RuntimeDecl{fullFX.decl, smallFX.decl}})
	manifest := packagetest.BuildManifestJSON(id, "1.0.0", []packagetest.InterfaceSpec{{ID: "quarantine.probe", Version: 1, SchemaFile: "interfaces/quarantine.probe.v1.schema.json", Methods: []string{"ping"}}}, []packagetest.VariantSpec{
		{ID: "full", Platform: packagefmt.HostPlatform(), Arch: packagefmt.HostArch(), Topology: packagefmt.HostTopology(), Runtime: "native_t3_component", Profile: "platform_reserved", Entrypoint: "variants/full/plugin"},
		{ID: "small", Platform: packagefmt.HostPlatform(), Arch: packagefmt.HostArch(), Topology: packagefmt.HostTopology(), Runtime: "native_t3_component", Profile: "platform_reserved", Entrypoint: "variants/small/plugin"},
	}, files, map[string]any{"aiios_min_version": floor, "default_variant": "full", "variant_preference": []string{"full", "small"}})
	spec := packagetest.PackageSpec{Root: id + "-1.0.0", Manifest: manifest, InstallFiles: files}
	if err := role.SignT3(&spec); err != nil {
		t.Fatal(err)
	}
	pkg := writePkg(t, spec)
	var fetched []string
	room := pluginfacility.Availability{HostKnown: true, HostTotal: 16 << 30, HostAvailable: 4 << 30}
	if constraint != "memory" {
		room.HostAvailable = 16 << 30
		room.Devices = map[string]pluginfacility.DeviceAvailability{"cuda": {Known: true, Total: 8 << 30, Available: 8 << 30}}
	}

	opts := &Options{Roots: roots, WorkerBinary: "unused-before-start", PathLimit: 1<<15 - 1, ResourceFacts: func(context.Context) (pluginfacility.Availability, error) { return room, nil },
		RuntimeLimits: operator}
	if constraint == "disk" {
		opts.FreeDisk = func(string) (int64, error) { return diskMargin + 1<<30, nil }
	}
	if extent {
		opts.HostVersion = packagefmt.RuntimeExtentMinHost
	}
	useStore(t, opts, func(ctx context.Context, url string, offset int64, w io.Writer) (int64, error) {
		fetched = append(fetched, url)
		switch url {
		case smallFX.decl.URL:
			return smallFX.fetch(ctx, url, offset, w)
		case fullFX.decl.URL:
			t.Errorf("unselected runtime fetched: %s", url)
			return 0, &FetchStatusError{Status: 404}
		}
		n, err := w.Write(weights[offset:])
		return int64(n), err
	})
	res, err := Verify(pkg, opts)
	if err != nil {
		t.Fatal(err)
	}
	oldOpts := *opts
	oldOpts.HostVersion = "0.1.11"
	if _, err := StagePreparedContext(t.Context(), pkg, res, &oldOpts); err == nil {
		t.Fatal("old host floor bypassed")
	}
	// A wrong signed-member digest is corruption, never lack of capacity.
	bad := *res
	bad.FileDigests = make(map[string]string, len(res.FileDigests))
	for name, digest := range res.FileDigests {
		bad.FileDigests[name] = digest
	}
	bad.FileDigests[AcceleratorFile] = "sha256:0000000000000000000000000000000000000000000000000000000000000000"
	if _, err := StagePreparedContext(t.Context(), pkg, &bad, opts); err == nil {
		t.Fatal("corrupt profile fell back")
	}
	bad.FileDigests[AcceleratorFile] = res.FileDigests[AcceleratorFile]
	bad.FileDigests[RuntimesFile] = "sha256:0000000000000000000000000000000000000000000000000000000000000000"
	if _, err := StagePreparedContext(t.Context(), pkg, &bad, opts); err == nil {
		t.Fatal("corrupt runtime declaration fell back")
	}
	if len(fetched) != 0 {
		t.Fatal("invalid candidate fetched material")
	}
	staged, err := StagePreparedContext(t.Context(), pkg, res, opts)
	if err != nil {
		t.Fatal(err)
	}
	if staged.Selection().VariantID != "small" || len(staged.Selection().ResourceRefusals) != 1 || len(fetched) != 0 {
		t.Fatalf("selection=%+v fetches=%v", staged.Selection(), fetched)
	}
	if extent {
		// The refusal names the ceiling to raise, and raising it chooses
		// the full set, still before any material moves.
		key, raised := "plugins.runtime.max_file_bytes", *opts
		raised.RuntimeLimits.MaxFileBytes = 600 << 20
		if constraint == "runtime-depth" {
			key = "plugins.runtime.max_depth"
			raised.RuntimeLimits = packagefmt.TreeLimits{MaxDepth: packagefmt.DefaultTreeLimits.MaxDepth + 1}
		}
		if why := strings.Join(staged.Selection().ResourceRefusals[0].Missing, "; "); !strings.Contains(why, key) {
			t.Fatalf("the full set's refusal does not name %s: %s", key, why)
		}
		full, err := StagePreparedContext(t.Context(), pkg, res, &raised)
		if err != nil || full.Selection().VariantID != "full" || len(fetched) != 0 {
			t.Fatalf("with %s raised: %v, %+v, fetched %v", key, err, full, fetched)
		}
	}
	if err := staged.Acquire(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(fetched) != 2 || fetched[0] != models[1].URL || fetched[1] != smallFX.decl.URL {
		t.Fatalf("wrong material: %v", fetched)
	}
	// Capacity changed while downloading: refusal precedes descriptors, broker
	// binding and spawn, and never silently swaps to a different component set.
	room.HostAvailable = 0
	_, err = staged.Start(t.Context(), newRegistry(t), false)
	var selection *VariantSelectionError
	if !errors.As(err, &selection) || pluginfacility.NewRefusal(id, "1.0.0", 1, pluginfacility.StageStart, err).Class != pluginfacility.ClassTransient {
		t.Fatalf("stale capacity reached start: %v", err)
	}
	if len(fetched) != 2 {
		t.Fatal("a start refusal downloaded another variant")
	}
}

func TestRuntimeDeclarationSelectionLimits(t *testing.T) {
	defaults := packagefmt.DefaultTreeLimits
	at := RuntimeDecl{Size: defaults.MaxCompressedBytes, InstalledBytes: defaults.MaxInstalledBytes, Files: defaults.MaxFiles}
	for _, tc := range []struct {
		name     string
		decl     RuntimeDecl
		limits   packagefmt.TreeLimits
		refusals int
	}{
		{"inclusive-default", at, packagefmt.TreeLimits{}, 0},

		{"over-all", RuntimeDecl{Size: at.Size + 1, InstalledBytes: at.InstalledBytes + 1, Files: at.Files + 1}, packagefmt.TreeLimits{}, 1},
		{"over-all-operator-set", RuntimeDecl{Size: at.Size + 1, InstalledBytes: at.InstalledBytes + 1, Files: at.Files + 1}, packagefmt.TreeLimits{MaxCompressedBytes: at.Size, MaxInstalledBytes: at.InstalledBytes}, 3},
		{"explicit-tighter", at, packagefmt.TreeLimits{MaxCompressedBytes: at.Size - 1, MaxInstalledBytes: at.InstalledBytes - 1, MaxFiles: at.Files - 1}, 3},
		{"explicit-raised", RuntimeDecl{Size: at.Size + 1, InstalledBytes: at.InstalledBytes + 1, Files: at.Files + 1}, packagefmt.TreeLimits{MaxCompressedBytes: at.Size + 1, MaxInstalledBytes: at.InstalledBytes + 1, MaxFiles: at.Files + 1}, 0},
		{"nonpositive-defaults", at, packagefmt.TreeLimits{MaxCompressedBytes: -1, MaxInstalledBytes: -1, MaxFiles: -1}, 0},
		{"member-depth-remain-extraction-owned", at, packagefmt.TreeLimits{MaxFileBytes: 1, MaxDepth: 1, MaxInventoryBytes: 1}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := runtimeDeclarationRefusals(tc.decl, tc.limits); len(got) != tc.refusals {
				t.Fatalf("refusals=%v want=%d", got, tc.refusals)
			}
		})
	}
}

func TestSelectionCancellationAndLegacyBehavior(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := selectionAvailability(ctx, &Options{})
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	ctx, cancel = context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err = selectionAvailability(ctx, &Options{ResourceFacts: func(ctx context.Context) (pluginfacility.Availability, error) {
		<-ctx.Done()
		return pluginfacility.Availability{}, ctx.Err()
	}})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	// Older packages do not start measurements or gain new memory refusals.
	pkg := writePkg(t, pkgSpec("org.example.legacyselect", "1.0.0", []string{"ping"}, fixtureWasm(t, "caller.wasm")))
	res, err := Verify(pkg, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = SelectContext(t.Context(), pkg, res, &Options{ResourceFacts: func(context.Context) (pluginfacility.Availability, error) {
		t.Fatal("legacy package measured")
		return pluginfacility.Availability{}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
}
