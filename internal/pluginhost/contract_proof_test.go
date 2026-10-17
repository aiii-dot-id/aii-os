package pluginhost

import (
	"context"
	"errors"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt/packagetest"
	"github.com/aiii-dot-id/aii-os/internal/pluginworker/wasmgen"
)

func describingPkg(t *testing.T, id string, packaged, artifact []byte) string {
	t.Helper()
	return describingPkgWith(t, id, packaged, wasmgen.DescribingResponder(artifact))
}

// describingPkgWith packages the given descriptor beside the given guest.
func describingPkgWith(t *testing.T, id string, packaged, wasm []byte, capabilities ...string) string {
	t.Helper()
	files := map[string][]byte{
		"interfaces/quarantine.probe.v1.schema.json": packaged,
		"variants/linux-x86_64-wasm/plugin.wasm":     wasm,
	}
	var extra map[string]any
	if len(capabilities) > 0 {
		extra = map[string]any{"capability_envelope": capabilities}
	}
	manifest := packagetest.BuildManifestJSON(id, "0.1.0",
		[]packagetest.InterfaceSpec{{ID: "quarantine.probe", Version: 1, SchemaFile: "interfaces/quarantine.probe.v1.schema.json", Methods: []string{"ping"}}},
		[]packagetest.VariantSpec{{
			ID: "linux-x86_64-wasm", Platform: packagefmt.HostPlatform(), Arch: packagefmt.HostArch(),
			Topology: packagefmt.HostTopology(), Runtime: "wasm_component", Profile: "wasm_sandbox",
			Entrypoint:   "variants/linux-x86_64-wasm/plugin.wasm",
			Capabilities: capabilities,
		}},
		files, extra)
	return writePkg(t, packagetest.PackageSpec{Root: id + "-0.1.0", Manifest: manifest, InstallFiles: files})
}

// The packaged descriptor must be the artifact's own account: equal
// activates, different refuses by name, and the comparison is by
// meaning, not bytes.
func TestThePackagedDescriptorMustBeTheArtifactsOwn(t *testing.T) {
	account := []byte(`[{"id":"ping","summary":"answers","input":"","output":"","effects":"read.internal","capabilities":[]}]`)
	reg := newRegistry(t)
	ap := activate(t, describingPkg(t, "org.example.proven", account, account), reg)
	if len(ap.ToolNames) != 1 {
		t.Fatalf("the proven package must activate, got tools %v", ap.ToolNames)
	}

	// Same meaning, different bytes: still the artifact's own account.
	reordered := []byte(`[{"capabilities":[],"effects":"read.internal","output":"","input":"","summary":"answers","id":"ping"}]`)
	activate(t, describingPkg(t, "org.example.reordered", reordered, account), newRegistry(t))

	// A packaged file that says more than the artifact does is refused.
	edited := []byte(`[{"id":"ping","summary":"answers","input":"","output":"","effects":"exec","capabilities":["ring4.kv"]}]`)
	_, err := Activate(context.Background(), describingPkg(t, "org.example.edited", edited, account), newRegistry(t), nil)
	var dme *DescriptorMismatchError
	if !errors.As(err, &dme) || dme.Interface != "quarantine.probe" || dme.Unasked || !dme.WaitsOnPackage() {
		t.Fatalf("an edited descriptor must be refused as not the artifact's own, waiting on the package, got %v", err)
	}
}

func TestAnArtifactThatCannotBeAskedIsNotThePackagesAnswer(t *testing.T) {
	account := []byte(`[{"id":"ping","summary":"answers","input":"","output":"","effects":"read.internal","capabilities":[]}]`)
	_, err := Activate(context.Background(), describingPkgWith(t, "org.example.unasked", account, wasmgen.TrappingDescriber()), newRegistry(t), nil)
	var dme *DescriptorMismatchError
	if !errors.As(err, &dme) || !dme.Unasked || dme.WaitsOnPackage() {
		t.Fatalf("a describe export that fails must refuse as unasked and retryable, got %v", err)
	}
}

// An artifact without the export keeps the older posture: the packaged
// file is taken as declared and the activation proceeds.
func TestAnArtifactWithoutDescribeIsUnprovenNotRefused(t *testing.T) {
	reg := newRegistry(t)
	ap := activate(t, contractPkg(t, "org.example.unproven", []byte(`[{"id":"ping","summary":"s"}]`), nil, nil), reg)
	if len(ap.ToolNames) != 1 {
		t.Fatal("an older artifact must still activate")
	}
}
