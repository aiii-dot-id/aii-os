package pluginhost

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt/packagetest"
	"github.com/aiii-dot-id/aii-os/internal/supervisor"
)

// .
// .
// .
// .
// .
// .
// .
// .

func platformRootsForTest(t *testing.T) (packagefmt.TrustRoots, *packagetest.Role) {
	t.Helper()
	root, status, err := packagetest.NewPlatformRelease("aiii_test_platform_release")
	if err != nil {
		t.Fatal(err)
	}
	trust := t.TempDir()
	if err := os.WriteFile(filepath.Join(trust, packagetest.StatusFilePlatform), status, 0o600); err != nil {
		t.Fatal(err)
	}
	roots := packagefmt.TrustRoots{PlatformRelease: root.Env}
	roots.Revocation = packagefmt.LoadRevocationStatus(trust, roots, nil)
	return roots, root
}

func hostVariantID() string {
	return packagefmt.HostPlatform() + "-" + packagefmt.HostArch() + "-native"
}

// .
// .
// .
func nativePackage(t *testing.T, id string, child []byte, root *packagetest.Role, runtimes []byte) string {
	t.Helper()
	return nativePackageWithModels(t, id, child, root, runtimes, nil)
}

// .
// .
func nativePackageWithModels(t *testing.T, id string, child []byte, root *packagetest.Role, runtimes, models []byte) string {
	t.Helper()
	vid := hostVariantID()
	files := map[string][]byte{
		"interfaces/quarantine.probe.v1.schema.json": []byte(`{"interface":"quarantine.probe","v":1}`),
		"variants/" + vid + "/child":                 child,
	}
	if runtimes != nil {
		files[RuntimesFile] = runtimes
	}
	if models != nil {
		files[ModelsFile] = models
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

func runtimesJSON(t *testing.T, fx *runtimeFixture) []byte {
	t.Helper()
	d := fx.decl
	d.VariantID = hostVariantID()
	raw, err := json.Marshal(map[string]interface{}{"runtimes": []RuntimeDecl{d}})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestRuntimeDeclarationRefusesTypedWhereItCannotBeMet(t *testing.T) {
	skipWhereNativeIsRefused(t)
	roots, role := platformRootsForTest(t)
	child, err := os.ReadFile(fakechildBin)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	fx := newRuntimeFixture(t)
	const id = "org.example.rtdecl"
	pkg := nativePackage(t, id, child, role, runtimesJSON(t, fx))

	// .
	var missing *RuntimeMissingError
	if _, err := Activate(ctx, pkg, newRegistry(t), supervisedOpts(Options{Roots: roots})); !errors.As(err, &missing) || !strings.Contains(err.Error(), "keeps no runtime directory") {
		t.Fatalf("a host without a runtime directory refuses typed: %v", err)
	}
	// .
	// .
	dir := t.TempDir()
	if _, err := Activate(ctx, pkg, newRegistry(t), supervisedOpts(Options{Roots: roots, PluginRuntimeDir: dir})); !errors.As(err, &missing) || !strings.Contains(err.Error(), "offline") {
		t.Fatalf("offline refuses typed: %v", err)
	}
	if n := publishedRoots(t, filepath.Join(dir, sanitizeToken(id))); n != 0 {
		t.Fatalf("a refusal publishes nothing: %d roots", n)
	}
	// .
	bad := nativePackage(t, "org.example.rtbad", child, role, []byte(`{"runtimes":[]}`))
	var rerr *RuntimeError
	if _, err := Activate(ctx, bad, newRegistry(t), supervisedOpts(Options{Roots: roots, PluginRuntimeDir: dir})); !errors.As(err, &rerr) {
		t.Fatalf("an empty declaration refuses typed: %v", err)
	}
}

func TestRuntimeRootIsPublishedPinnedLaunchedFromAndReleased(t *testing.T) {
	skipWhereNativeIsRefused(t)
	// .
	// .
	// .
	// .
	// .
	skipWhereTheSandboxCannotBeEstablished(t)
	roots, role := platformRootsForTest(t)
	child, err := os.ReadFile(fakechildBin)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	const id = "org.example.rtlaunch"
	fx := newRuntimeFixtureWith(t, "# one\n")
	pkg := nativePackage(t, id, child, role, runtimesJSON(t, fx))
	dir := t.TempDir()
	registry := NewRuntimeRoots()
	sink := &logSink{}
	opts := supervisedOpts(Options{Roots: roots, PluginRuntimeDir: dir, RuntimeFetcher: fx.fetch, RuntimeRoots: registry, RuntimeRootsKept: 1, Log: log.New(sink, "", 0)})
	pluginDir := filepath.Join(dir, sanitizeToken(id))
	root := filepath.Join(pluginDir, RuntimeRootKey(hostVariantID(), fx.decl.InventorySHA256, "child", digestOf(child)))

	ap, err := Activate(ctx, pkg, newRegistry(t), opts)
	if err != nil {
		// .
		// .
		// .
		if !strings.Contains(err.Error(), "native lane unavailable") && !strings.Contains(err.Error(), "cannot contain") {
			t.Fatalf("activation: %v", err)
		}
		if _, serr := os.Stat(filepath.Join(root, "child")); serr != nil {
			t.Fatalf("the tree is published before the launch is attempted: %v", serr)
		}
		if n := registry.Refs(root); n != 0 {
			t.Fatalf("a failed launch releases its pin: refs = %d", n)
		}
		t.Skipf("the launch itself needs a host that can contain a native child: %v", err)
	}
	released := false
	defer func() {
		if !released {
			_ = ap.Deactivate(ctx)
		}
	}()
	if ap.RuntimeRoot != root {
		t.Fatalf("the activation runs from %q, want %q", ap.RuntimeRoot, root)
	}
	if ap.artifactDir != "" {
		t.Fatalf("no extracted artifact exists beside a runtime root: %q", ap.artifactDir)
	}
	if n := registry.Refs(root); n != 1 || fx.fetches != 1 {
		t.Fatalf("pinned once, fetched once: refs %d fetches %d", n, fx.fetches)
	}
	// .
	if !waitUntil(func() bool {
		return strings.Contains(sink.String(), `runtime_root="`+root+`"`)
	}) {
		t.Fatalf("timed out waiting for the child to report runtime root %q; the child's own log:\n%s", root, sink.String())
	}
	if !strings.Contains(sink.String(), "runtime root "+root) {
		t.Fatalf("the containment line names the root:\n%s", sink.String())
	}
	resp, err := ap.sup.Invoke(ctx, []byte(`{"jsonrpc":"2.0","id":"h1","method":"invoke.call","params":{}}`))
	if err != nil || !strings.Contains(string(resp), `"answered":true`) {
		t.Fatalf("the child answers from its root: %v %s", err, resp)
	}
	// .
	// .
	if err := os.WriteFile(filepath.Join(root, "engine", "session.py"), []byte("print('evil')\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	proc, _ := os.FindProcess(ap.sup.Pid())
	_ = proc.Kill()
	pollUntil(t, "tamper stop", func() bool {
		state, _ := ap.sup.State()
		return state == supervisor.StateStopped
	})
	_, reason := ap.sup.State()
	var refused *supervisor.SpawnRefusedError
	if !errors.As(reason, &refused) || !strings.Contains(reason.Error(), "session.py") {
		t.Fatalf("a tampered tree stops the supervisor typed, naming the file: %v", reason)
	}
	// .
	if err := ap.Deactivate(ctx); err != nil {
		t.Fatalf("deactivate: %v", err)
	}
	released = true
	if n := registry.Refs(root); n != 0 {
		t.Fatalf("released: refs = %d", n)
	}
	if _, err := os.Stat(root); err != nil {
		t.Fatalf("the root stays for the next activation: %v", err)
	}
	// .
	// .
	fx2 := newRuntimeFixtureWith(t, "# two\n")
	pkg2 := nativePackage(t, id, child, role, runtimesJSON(t, fx2))
	opts2 := *opts
	opts2.RuntimeFetcher = fx2.fetch
	ap2, err := Activate(ctx, pkg2, newRegistry(t), &opts2)
	if err != nil {
		t.Fatalf("the second release activates: %v", err)
	}
	defer ap2.Deactivate(ctx)
	root2 := filepath.Join(pluginDir, RuntimeRootKey(hostVariantID(), fx2.decl.InventorySHA256, "child", digestOf(child)))
	if ap2.RuntimeRoot != root2 || registry.Refs(root2) != 1 {
		t.Fatalf("the second activation runs pinned from %q: %q refs %d", root2, ap2.RuntimeRoot, registry.Refs(root2))
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("the older unpinned root is retired past the newest kept: %v", err)
	}
}
