package pluginhost

import (
	"context"
	"errors"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/broker"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt/packagetest"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// THE STAGES ARE THE ACTIVATION'S OWN. The facility sequences Verify and
// Select itself so it can say which stage an attempt is in. That is only
// safe while the stages answer exactly what an activation answers — a
// second, divergent implementation of "what would this host run" is how
// a facility comes to report one thing and run another.
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
	// A wasm package declares no accelerator profile, so it reserves
	// nothing in particular and its device figure is UNKNOWN, not zero.
	if sel.HostBytes != 0 || sel.DeviceBytes != nil || sel.Backend != "" {
		t.Fatalf("a package with no profile must declare no reservation: %+v", sel)
	}
	if sel.Startup.Effective <= 0 {
		t.Fatalf("every selection must carry an allowance, got %v", sel.Startup.Effective)
	}
}

// A HOST THE PACKAGE EXCLUDES IS ANSWERED AT VERIFICATION, before a
// variant is chosen — the stage that reads the package's own word about
// which hosts it runs on, and the one whose refusal is permanent until
// the package or the host changes.
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
		files, map[string]any{"aiios_min_version": "99.0.0"})
	pkg := writePkg(t, packagetest.PackageSpec{Root: "org.example.future-0.1.0", Manifest: manifest, InstallFiles: files})

	_, err := Verify(pkg, nil)
	var hv *HostVersionError
	if !errors.As(err, &hv) {
		t.Fatalf("a package this host is too old for must refuse at verification, got %v", err)
	}
	if hv.Min != "99.0.0" {
		t.Fatalf("the refusal must name the window it declared, got %+v", hv)
	}
	// And the whole activation refuses the same way, at the same stage.
	if _, aerr := Activate(context.Background(), pkg, newRegistry(t), nil); !errors.As(aerr, &hv) {
		t.Fatalf("the activation must refuse where the stage does, got %v", aerr)
	}
}

// SELECTION PRECEDES THE BINDING, and stays that way. A package with no
// runnable variant must not reach the broker: building a binding clears
// the plugin's temp-scoped RING4 rows, and a plugin this host cannot run
// has no business having its working set touched.
func TestAPackageWithNoRunnableVariantNeverReachesTheBroker(t *testing.T) {
	const id = "org.example.aot"
	st := newBrokerStore(t)
	h, err := broker.New(broker.Config{Store: st, Grants: map[string]broker.Grant{id: {KV: true}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.PluginKVPut(store.PluginScope{PluginID: id}, "working-set", "what a previous activation held", true, 64, 1<<20); err != nil {
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

func assertWorkingSetSurvived(t *testing.T, st *store.Store, id, want string) {
	t.Helper()
	got, ok, err := st.PluginKVGet(store.PluginScope{PluginID: id}, "working-set")
	if err != nil {
		t.Fatal(err)
	}
	if !ok || got != want {
		t.Fatalf("the refusal cleared %s's temp scope: it bound a plugin it then refused (ok=%v value=%q)", id, ok, got)
	}
}

// STAGING AND STARTING IS ACTIVATING. Activate is the composition of the
// stages, so a caller that runs them itself — the facility, which does
// so to report where an attempt is — must arrive at the same activation,
// registering the same tools from the same evidence.
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

	// A caller that already verified the package reaches the same place
	// without re-reading the signature.
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

// THE HOST WINDOW IS NOT THE CALLER'S TO SKIP. The memoized entry point
// takes a verification result the caller already holds — and that result
// came from packagefmt, which answers whether the package is VALID and
// says nothing about whether this host is one it declares it runs on.
// Treating it as permission to proceed would have made the entry point
// the application uses for every package it sweeps a way around the
// window.
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
		files, map[string]any{"aiios_min_version": "99.0.0"})
	pkg := writePkg(t, packagetest.PackageSpec{Root: "org.example.toonew-0.1.0", Manifest: manifest, InstallFiles: files})

	// The caller's own verification, exactly as the application memoizes
	// it: valid package, nothing said about this host.
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
