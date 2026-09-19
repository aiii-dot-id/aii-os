package pluginhost

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/broker"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt/packagetest"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// .
// .
// .
// .
// .
func TestTheStagesAnswerWhatAnActivationDoes(t *testing.T) {
	reg := newRegistry(t)
	pkg := writePkg(t, pkgSpec("org.example.stage", "0.1.0", []string{"ping"}, fixtureWasm(t, "caller.wasm")))

	res, err := Verify(pkg, nil)
	if err != nil {
		t.Fatalf("verification of a good package: %v", err)
	}
	sel, serr := Select(pkg, res, nil)
	if serr != nil {
		t.Fatalf("selection for this host: %v", serr)
	}

	ap := activate(t, pkg, reg)
	if ap.VariantID != sel.VariantID {
		t.Fatalf("the stage chose %q and the activation ran %q", sel.VariantID, ap.VariantID)
	}
	if ap.PackageHash != res.PackageHash {
		t.Fatalf("the stage verified %q and the activation ran %q", res.PackageHash, ap.PackageHash)
	}
	if sel.Runtime != "wasm_component" {
		t.Fatalf("the selection must name the lane it chose, got %q", sel.Runtime)
	}
	// .
	// .
	if sel.HostBytes != 0 || sel.DeviceBytes != nil || sel.Backend != "" {
		t.Fatalf("a package with no profile must declare no reservation: %+v", sel)
	}
	if sel.Startup.Effective <= 0 {
		t.Fatalf("every selection must carry an allowance, got %v", sel.Startup.Effective)
	}
}

// .
// .
// .
// .
func TestVerifyAnswersTheHostWindowBeforeAVariantIsChosen(t *testing.T) {
	files := map[string][]byte{
		"interfaces/quarantine.probe.v1.schema.json": []byte(`{"interface":"quarantine.probe","v":1}`),
		"variants/only/plugin.wasm":                  fixtureWasm(t, "caller.wasm"),
	}
	manifest := packagetest.BuildManifestJSON("org.example.future", "0.1.0",
		[]packagetest.InterfaceSpec{{
			ID: "quarantine.probe", Version: 1,
			SchemaFile: "interfaces/quarantine.probe.v1.schema.json",
			Methods:    []string{"ping"},
		}},
		[]packagetest.VariantSpec{{
			ID: "only", Platform: packagefmt.HostPlatform(), Arch: packagefmt.HostArch(),
			Topology: packagefmt.HostTopology(), Runtime: "wasm_component", Profile: "wasm_sandbox",
			Entrypoint: "variants/only/plugin.wasm",
		}},
		files, map[string]interface{}{"aiios_min_version": "99.0.0"})
	pkg := writePkg(t, packagetest.PackageSpec{Root: "org.example.future-0.1.0", Manifest: manifest, InstallFiles: files})

	_, err := Verify(pkg, nil)
	var hv *HostVersionError
	if !errors.As(err, &hv) {
		t.Fatalf("a package this host is too old for must refuse at verification, got %v", err)
	}
	if hv.Min != "99.0.0" {
		t.Fatalf("the refusal must name the window it declared, got %+v", hv)
	}
	// .
	if _, aerr := Activate(context.Background(), pkg, newRegistry(t), nil); !errors.As(aerr, &hv) {
		t.Fatalf("the activation must refuse where the stage does, got %v", aerr)
	}
}

// .
// .
// .
// .
func TestAPackageWithNoRunnableVariantNeverReachesTheBroker(t *testing.T) {
	const id = "org.example.aot"
	st := newBrokerStore(t)
	h, err := broker.New(broker.Config{Store: st, Grants: map[string]broker.Grant{id: {KV: true}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.PluginKVPut(id, "working-set", "what a previous activation held", true, 64, 1<<20); err != nil {
		t.Fatal(err)
	}

	files := map[string][]byte{
		"interfaces/quarantine.probe.v1.schema.json": []byte(`{"interface":"quarantine.probe","v":1}`),
		"variants/only/plugin.bin":                   []byte("\x00asm\x01\x00\x00\x00stub"),
	}
	manifest := packagetest.BuildManifestJSON(id, "0.1.0",
		[]packagetest.InterfaceSpec{{
			ID: "quarantine.probe", Version: 1,
			SchemaFile: "interfaces/quarantine.probe.v1.schema.json",
			Methods:    []string{"ping"},
		}},
		[]packagetest.VariantSpec{{
			ID: "only", Platform: packagefmt.HostPlatform(), Arch: packagefmt.HostArch(),
			Topology: packagefmt.HostTopology(), Runtime: "wasm_aot_component", Profile: "wasm_sandbox",
			Entrypoint: "variants/only/plugin.bin",
		}},
		files, nil)
	pkg := writePkg(t, packagetest.PackageSpec{Root: id + "-0.1.0", Manifest: manifest, InstallFiles: files})

	_, aerr := Activate(context.Background(), pkg, newRegistry(t), &Options{Broker: h})
	var serr *VariantSelectionError
	if !errors.As(aerr, &serr) {
		t.Fatalf("an AOT-only package must refuse at selection, got %v", aerr)
	}
	assertWorkingSetSurvived(t, st, id, "what a previous activation held")
}

// .
// .
// .
// .
// .
// .
func TestMaterialIsSettledBeforeTheBrokerIsTouched(t *testing.T) {
	skipWhereNativeIsRefused(t)
	roots, role := platformRootsForTest(t)
	child, err := os.ReadFile(fakechildBin)
	if err != nil {
		t.Fatal(err)
	}
	const id = "org.example.rtbind"
	st := newBrokerStore(t)
	h, berr := broker.New(broker.Config{Store: st, Grants: map[string]broker.Grant{id: {KV: true}}})
	if berr != nil {
		t.Fatal(berr)
	}
	if err := st.PluginKVPut(id, "working-set", "what the running release holds", true, 64, 1<<20); err != nil {
		t.Fatal(err)
	}

	fx := newRuntimeFixture(t)
	pkg := nativePackage(t, id, child, role, runtimesJSON(t, fx))
	// .
	// .
	// .
	_, aerr := Activate(context.Background(), pkg, newRegistry(t), supervisedOpts(Options{Roots: roots, Broker: h}))
	var missing *RuntimeMissingError
	if !errors.As(aerr, &missing) {
		t.Fatalf("an unmeetable runtime declaration must refuse typed, got %v", aerr)
	}
	assertWorkingSetSurvived(t, st, id, "what the running release holds")
}

func assertWorkingSetSurvived(t *testing.T, st *store.Store, id, want string) {
	t.Helper()
	got, ok, err := st.PluginKVGet(id, "working-set")
	if err != nil {
		t.Fatal(err)
	}
	if !ok || got != want {
		t.Fatalf("the refusal cleared %s's temp scope: it bound a plugin it then refused (ok=%v value=%q)", id, ok, got)
	}
}

// .
// .
// .
// .
func TestAStagedStartIsAnActivation(t *testing.T) {
	ctx := context.Background()
	pkg := writePkg(t, pkgSpec("org.example.staged", "0.1.0", []string{"ping"}, fixtureWasm(t, "caller.wasm")))

	st, err := Stage(ctx, pkg, nil)
	if err != nil {
		t.Fatalf("staging a good package: %v", err)
	}
	if st.ID() != "org.example.staged" || st.Version() != "0.1.0" || st.Package() != pkg {
		t.Fatalf("a staged package must name itself: %s %s %s", st.ID(), st.Version(), st.Package())
	}
	staged, err := st.Start(ctx, newRegistry(t), true)
	if err != nil {
		t.Fatalf("starting what was staged: %v", err)
	}
	defer staged.Deactivate(ctx)

	whole := activate(t, pkg, newRegistry(t))
	if staged.VariantID != whole.VariantID || staged.PackageHash != whole.PackageHash || staged.Tier != whole.Tier {
		t.Fatalf("the staged start and the whole activation disagree: %+v vs %+v", staged, whole)
	}
	if len(staged.ToolNames) == 0 || len(staged.ToolNames) != len(whole.ToolNames) {
		t.Fatalf("both must admit the same tools: %v vs %v", staged.ToolNames, whole.ToolNames)
	}

	// .
	// .
	res, verr := Verify(pkg, nil)
	if verr != nil {
		t.Fatal(verr)
	}
	memoized, merr := StageVerified(ctx, pkg, res, nil)
	if merr != nil {
		t.Fatalf("staging from a verification already held: %v", merr)
	}
	if memoized.PackageHash() != st.PackageHash() || memoized.Selection().VariantID != st.Selection().VariantID {
		t.Fatalf("the memoized entry point must stage the same package the same way")
	}
}

// .
// .
// .
// .
// .
// .
// .
func TestEveryStagingPathEnforcesTheHostWindow(t *testing.T) {
	files := map[string][]byte{
		"interfaces/quarantine.probe.v1.schema.json": []byte(`{"interface":"quarantine.probe","v":1}`),
		"variants/only/plugin.wasm":                  fixtureWasm(t, "caller.wasm"),
	}
	manifest := packagetest.BuildManifestJSON("org.example.toonew", "0.1.0",
		[]packagetest.InterfaceSpec{{ID: "quarantine.probe", Version: 1,
			SchemaFile: "interfaces/quarantine.probe.v1.schema.json", Methods: []string{"ping"}}},
		[]packagetest.VariantSpec{{ID: "only", Platform: packagefmt.HostPlatform(), Arch: packagefmt.HostArch(),
			Topology: packagefmt.HostTopology(), Runtime: "wasm_component", Profile: "wasm_sandbox",
			Entrypoint: "variants/only/plugin.wasm"}},
		files, map[string]interface{}{"aiios_min_version": "99.0.0"})
	pkg := writePkg(t, packagetest.PackageSpec{Root: "org.example.toonew-0.1.0", Manifest: manifest, InstallFiles: files})

	// .
	// .
	res, err := packagefmt.VerifyFile(pkg, packagefmt.TrustRoots{})
	if err != nil {
		t.Fatalf("the package itself must be valid, or this proves nothing: %v", err)
	}
	var hv *HostVersionError
	if _, serr := StagePrepared(pkg, res, nil); !errors.As(serr, &hv) {
		t.Fatalf("StagePrepared must enforce the window, got %v", serr)
	}
	if _, serr := StageVerified(context.Background(), pkg, res, nil); !errors.As(serr, &hv) {
		t.Fatalf("StageVerified must enforce the window, got %v", serr)
	}
	if _, serr := Stage(context.Background(), pkg, nil); !errors.As(serr, &hv) {
		t.Fatalf("Stage must enforce the window, got %v", serr)
	}
}

// .
// .
// .
// .
// .
func TestAStagedPackageWithMaterialIsNeverAlreadyPresent(t *testing.T) {
	// .
	plain := writePkg(t, pkgSpec("org.example.nomaterial", "0.1.0", []string{"ping"}, fixtureWasm(t, "caller.wasm")))
	st, err := Stage(context.Background(), plain, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !st.Present() {
		t.Fatal("a lane that declares no material has nothing to acquire")
	}
	if st.Material().PluginID != "" {
		t.Fatalf("and declares none: %+v", st.Material())
	}

	// .
	// .
	skipWhereNativeIsRefused(t)
	roots, role := platformRootsForTest(t)
	child, rerr := os.ReadFile(fakechildBin)
	if rerr != nil {
		t.Fatal(rerr)
	}
	fx := newRuntimeFixture(t)
	pkg := nativePackage(t, "org.example.hasmaterial", child, role, runtimesJSON(t, fx))
	res, verr := Verify(pkg, supervisedOpts(Options{Roots: roots}))
	if verr != nil {
		t.Fatal(verr)
	}
	prepared, perr := StagePrepared(pkg, res, supervisedOpts(Options{Roots: roots, PluginRuntimeDir: t.TempDir()}))
	if perr != nil {
		t.Fatalf("preparing a native package: %v", perr)
	}
	if prepared.Material().PluginID == "" {
		t.Fatalf("precondition: this package must declare material")
	}
	if prepared.Present() {
		t.Fatal("a lane with declared material must be acquired: acquisition is what publishes its runtime root")
	}
}

// .
// .
// .
// .
// .
// .
func TestStartingUnacquiredMaterialRefusesBeforeAnythingIsBound(t *testing.T) {
	skipWhereNativeIsRefused(t)
	roots, role := platformRootsForTest(t)
	child, err := os.ReadFile(fakechildBin)
	if err != nil {
		t.Fatal(err)
	}
	const id = "org.example.unacquired"
	st := newBrokerStore(t)
	h, berr := broker.New(broker.Config{Store: st, Grants: map[string]broker.Grant{id: {KV: true}}})
	if berr != nil {
		t.Fatal(berr)
	}
	if err := st.PluginKVPut(id, "working-set", "what the running release holds", true, 64, 1<<20); err != nil {
		t.Fatal(err)
	}

	fx := newRuntimeFixture(t)
	pkg := nativePackage(t, id, child, role, runtimesJSON(t, fx))
	opts := supervisedOpts(Options{Roots: roots, Broker: h, PluginRuntimeDir: t.TempDir()})
	res, verr := Verify(pkg, opts)
	if verr != nil {
		t.Fatal(verr)
	}
	staged, perr := StagePrepared(pkg, res, opts)
	if perr != nil {
		t.Fatalf("preparing: %v", perr)
	}
	if staged.Present() {
		t.Fatal("precondition: this package declares material")
	}

	// .
	var missing *NotAcquiredError
	if _, serr := staged.Start(context.Background(), newRegistry(t), true); !errors.As(serr, &missing) {
		t.Fatalf("a start with unacquired material must refuse typed, got %v", serr)
	}
	assertWorkingSetSurvived(t, st, id, "what the running release holds")
}
