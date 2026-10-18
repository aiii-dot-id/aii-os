//go:build (linux && !android) || (darwin && !ios) || windows

package pluginhost

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt/packagetest"
)

// The runtime trees the native stage and activation tests run from: built
// where the plugin store's writer runs.

func runtimesJSON(t *testing.T, fx *runtimeFixture) []byte {
	t.Helper()
	d := fx.decl
	d.VariantID = hostVariantID()
	raw, err := json.Marshal(map[string]any{"runtimes": []RuntimeDecl{d}})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// runtimeFixture is a runtime tree packed as its archive, its declaration
// and a carrier, served by fetch, which counts what it was asked and
// answers failWith while one is set.
type runtimeFixture struct {
	archive  []byte
	decl     RuntimeDecl
	entry    entrypointSpec
	inv      []byte
	fetches  int
	failWith error
}

func newRuntimeFixture(t *testing.T) *runtimeFixture { return newRuntimeFixtureWith(t, "") }

// newRuntimeFixtureWith salts one file so two fixtures are two trees
// with two inventories.
func newRuntimeFixtureWith(t *testing.T, salt string) *runtimeFixture {
	t.Helper()
	return newRuntimeFixtureOf(t, map[string][]byte{
		"python/python.exe": bytes.Repeat([]byte("py"), 3000),
		"lib/libtorch.dll":  bytes.Repeat([]byte("torch"), 4000),
		"engine/session.py": []byte("print('ready')\n" + salt),
	})
}

// newRuntimeFixtureOf is a runtime tree of exactly these files; one whose
// name ends in .exe is executable.
func newRuntimeFixtureOf(t *testing.T, files map[string][]byte) *runtimeFixture {
	t.Helper()
	src := t.TempDir()
	for rel, content := range files {
		p := filepath.Join(src, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		mode := os.FileMode(0o644)
		if strings.HasSuffix(rel, ".exe") {
			mode = 0o755
		}
		if err := os.WriteFile(p, content, mode); err != nil {
			t.Fatal(err)
		}
	}
	var buf bytes.Buffer
	inv, sha, err := packagefmt.WriteTree(&buf, src, "cp1", packagefmt.TreeLimits{})
	if err != nil {
		t.Fatal(err)
	}
	invSum := sha256.Sum256(inv)
	carrier := []byte("#!carrier\n")
	csum := sha256.Sum256(carrier)
	var total int64
	for _, c := range files {
		total += int64(len(c))
	}
	return &runtimeFixture{
		archive: buf.Bytes(),
		decl: RuntimeDecl{VariantID: "windows-x86_64-native", URL: "https://example.invalid/cp1.tar.gz", SHA256: strings.TrimPrefix(sha, "sha256:"),
			Size: int64(buf.Len()), InstalledBytes: total, Files: len(files), InventorySHA256: hex.EncodeToString(invSum[:])},
		entry: entrypointSpec{Name: "aii-voice.exe", Bytes: carrier, Digest: "sha256:" + hex.EncodeToString(csum[:])},
		inv:   inv,
	}
}

func (f *runtimeFixture) fetch(ctx context.Context, url string, offset int64, w io.Writer) (int64, error) {
	f.fetches++
	if f.failWith != nil {
		return 0, f.failWith
	}
	if offset > int64(len(f.archive)) {
		return 0, fmt.Errorf("offset past the end")
	}
	n, err := w.Write(f.archive[offset:])
	return int64(n), err
}

// componentSets is a signed package with two native component sets in the
// publisher's order — full (8 GiB host, 6 GiB on cuda) before small (1 GiB,
// no device) — each with its own model and runtime tree.
type componentSets struct {
	id, pkg         string
	roots           packagefmt.TrustRoots
	full, small     *runtimeFixture
	weights         []byte
	fullURL, smlURL string
}

// The platform role and each version's signed package are made once per
// test binary: SLH-DSA keygen and signing take seconds — about fourteen under
// the race detector — and a signed package is immutable (broker_e2e_test's
// signer, the same reasoning). Each test gets its own copy of the bytes; of
// the runtime fixtures only their bytes are shared.
var signedComponentSets = struct {
	sync.Mutex
	roots packagefmt.TrustRoots
	role  *packagetest.Role
	sets  map[string]componentSets
	raw   map[string][]byte
}{sets: map[string]componentSets{}, raw: map[string][]byte{}}

// componentSetsFor is the signed package of one version, written for t.
func componentSetsFor(t *testing.T, version string) componentSets {
	t.Helper()
	s := &signedComponentSets
	s.Lock()
	defer s.Unlock()
	if s.role == nil {
		s.roots, s.role = platformRootsForTest(t)
	}
	c, ok := s.sets[version]
	if !ok {
		c = newComponentSets(t, s.roots, s.role, version)
		raw, err := os.ReadFile(c.pkg)
		if err != nil {
			t.Fatal(err)
		}
		s.sets[version], s.raw[version] = c, raw
	}
	c.pkg = filepath.Join(t.TempDir(), filepath.Base(c.pkg))
	if err := os.WriteFile(c.pkg, s.raw[version], 0o644); err != nil {
		t.Fatal(err)
	}
	return c
}

func newComponentSets(t *testing.T, roots packagefmt.TrustRoots, role *packagetest.Role, version string) componentSets {
	t.Helper()
	const id = "org.example.componentsets"
	c := componentSets{id: id, roots: roots, full: newRuntimeFixtureWith(t, "full"), small: newRuntimeFixtureWith(t, "small"),
		weights: []byte("one verified model"), fullURL: "https://example.com/full-model", smlURL: "https://example.com/small-model"}
	c.full.decl.VariantID, c.small.decl.VariantID = "full", "small"
	c.full.decl.URL, c.small.decl.URL = "https://example.com/full-runtime", "https://example.com/small-runtime"
	files := map[string][]byte{"interfaces/quarantine.probe.v1.schema.json": []byte(`{"interface":"quarantine.probe","v":1}`),
		"variants/full/plugin": c.full.entry.Bytes, "variants/small/plugin": c.small.entry.Bytes}
	sum := sha256.Sum256(c.weights)
	files[ModelsFile], _ = json.Marshal([]ModelDecl{{Name: "full-model", URL: c.fullURL, SHA256: hex.EncodeToString(sum[:]), Size: int64(len(c.weights))},
		{Name: "small-model", URL: c.smlURL, SHA256: hex.EncodeToString(sum[:]), Size: int64(len(c.weights))}})
	profiles := map[string]AcceleratorProfile{"full": preferenceProfile(8<<30, 6<<30, "cuda"), "small": preferenceProfile(1<<30, 0)}
	for key, p := range profiles {
		p.OS, p.Arch, p.Models = packagefmt.HostPlatform(), packagefmt.HostArch(), []string{key + "-model"}
		profiles[key] = p
	}
	files[AcceleratorFile], _ = json.Marshal(profiles)
	files[RuntimesFile], _ = json.Marshal(map[string]any{"runtimes": []RuntimeDecl{c.full.decl, c.small.decl}})
	host := func(variant string) packagetest.VariantSpec {
		return packagetest.VariantSpec{ID: variant, Platform: packagefmt.HostPlatform(), Arch: packagefmt.HostArch(), Topology: packagefmt.HostTopology(),
			Runtime: "native_t3_component", Profile: "platform_reserved", Entrypoint: "variants/" + variant + "/plugin"}
	}
	manifest := packagetest.BuildManifestJSON(id, version, []packagetest.InterfaceSpec{{ID: "quarantine.probe", Version: 1, SchemaFile: "interfaces/quarantine.probe.v1.schema.json", Methods: []string{"ping"}}},
		[]packagetest.VariantSpec{host("full"), host("small")}, files,
		map[string]any{"aiios_min_version": packagefmt.VariantPreferenceMinHost, "default_variant": "full", "variant_preference": []string{"full", "small"}})
	spec := packagetest.PackageSpec{Root: id + "-" + version, Manifest: manifest, InstallFiles: files}
	if err := role.SignT3(&spec); err != nil {
		t.Fatal(err)
	}
	c.pkg = writePkg(t, spec)
	return c
}

// nativePackage signs a native T3 package for THIS host around the
// supervisor suite's fakechild, carrying the runtime declaration when
// one is given.
func nativePackage(t *testing.T, id string, child []byte, root *packagetest.Role, runtimes []byte) string {
	t.Helper()
	return nativePackageWithModels(t, id, child, root, runtimes, nil)
}

// nativePackageWithModels is nativePackage carrying a models
// declaration too, when one is given.
func nativePackageWithModels(t *testing.T, id string, child []byte, root *packagetest.Role, runtimes, models []byte) string {
	t.Helper()
	extra := map[string][]byte{}
	if runtimes != nil {
		extra[RuntimesFile] = runtimes
	}
	if models != nil {
		extra[ModelsFile] = models
	}
	return nativePackageWithFiles(t, id, child, root, extra)
}

// nativePackageWithFiles is nativePackage carrying the given install
// members (a runtime, models or accelerator declaration) beside the child.
func nativePackageWithFiles(t *testing.T, id string, child []byte, root *packagetest.Role, extra map[string][]byte) string {
	t.Helper()
	vid := hostVariantID()
	files := map[string][]byte{
		"interfaces/quarantine.probe.v1.schema.json": []byte(`{"interface":"quarantine.probe","v":1}`),
		"variants/" + vid + "/child":                 child,
	}
	for name, raw := range extra {
		files[name] = raw
	}
	manifest := packagetest.BuildManifestJSON(id, "0.1.0",
		[]packagetest.InterfaceSpec{{ID: "quarantine.probe", Version: 1, SchemaFile: "interfaces/quarantine.probe.v1.schema.json", Methods: []string{"ping"}}},
		[]packagetest.VariantSpec{{ID: vid, Platform: packagefmt.HostPlatform(), Arch: packagefmt.HostArch(), Topology: packagefmt.HostTopology(),
			Runtime: "native_t3_component", Profile: "platform_reserved", Entrypoint: "variants/" + vid + "/child"}},
		files, nil)
	spec := packagetest.PackageSpec{Root: id + "-0.1.0", Manifest: manifest, InstallFiles: files}
	if err := root.SignT3(&spec); err != nil {
		t.Fatal(err)
	}
	return writePkg(t, spec)
}

func preferenceProfile(host, device int64, required ...string) AcceleratorProfile {
	return AcceleratorProfile{OS: "linux", Arch: "x86_64", Backend: "cpu", Precision: "fp32", Models: []string{"m"}, MemoryBytes: host, DeviceMemoryBytes: &device, RequiredAccelerators: required, SessionLimit: 1, Fallback: "none"}
}
