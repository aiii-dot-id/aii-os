package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt/packagetest"
	"github.com/aiii-dot-id/aii-os/internal/plugincatalog"
)

// THE INSTALL PRESS IS THE GRANT. Memory and Planning installed from the
// catalog activated
// "quarantine, no operator grant", and every Memory call was refused until
// the operator found the toggles. A new install now grants what the signed
// envelope asks for of the identity's own storage and records (here kv and
// memory) and nothing else it asks (here an outbound host), recorded in
// plugins.grants as the toggles would.
func TestACatalogInstallGrantsWhatThePackageAsksOfTheIdentitysRecords(t *testing.T) {
	dir := catalogInstallDir(t)
	const id = "org.example.notes"
	wasm, err := os.ReadFile(filepath.Join("..", "pluginworker", "testdata", "responder.wasm"))
	if err != nil {
		t.Fatal(err)
	}
	asked := []string{"ring4.kv", "ring4.memory", "net.outbound"}
	files := map[string][]byte{
		"interfaces/quarantine.probe.v1.schema.json": []byte(`{"interface":"quarantine.probe","v":1}`),
		"variants/linux-x86_64-wasm/plugin.wasm":     wasm,
	}
	manifest := packagetest.BuildManifestJSON(id, "1.0.0",
		[]packagetest.InterfaceSpec{{ID: "quarantine.probe", Version: 1, SchemaFile: "interfaces/quarantine.probe.v1.schema.json", Methods: []string{"ping"}}},
		[]packagetest.VariantSpec{{ID: "linux-x86_64-wasm", Platform: packagefmt.HostPlatform(), Arch: packagefmt.HostArch(),
			Topology: packagefmt.HostTopology(), Runtime: "wasm_component", Profile: "wasm_sandbox",
			Entrypoint: "variants/linux-x86_64-wasm/plugin.wasm", Capabilities: asked}},
		files, map[string]any{"capability_envelope": asked})
	pkg := packagetest.Build(packagetest.PackageSpec{Root: id + "-1.0.0", Manifest: manifest, InstallFiles: files})
	url, sha := servePackage(t, pkg)
	t.Chdir(dir)
	app := startCatalogApp(t, dir, plugincatalog.Index{Version: 1, Plugins: []plugincatalog.CatalogEntry{
		{ID: id, Version: "1.0.0", Tier: "T1", Summary: "notes",
			Packages: []plugincatalog.CatalogPackage{{Platform: "*", Arch: "*", URL: url, SHA256: sha, Size: int64(len(pkg))}}}}})
	app.pkgFetch = loopbackFetcher()

	if err := app.InstallFromCatalog(context.Background(), id); err != nil {
		t.Fatalf("install: %v", err)
	}
	g, ok := app.configSnapshot().Plugins.Grants[id]
	if !ok || !g.KV || !g.Memory {
		t.Fatalf("the install must grant kv and memory: %+v (listed %v)", g, ok)
	}
	if len(g.Hosts) != 0 || g.Interactions || g.Embeddings || g.Voice || g.Tools || g.Files {
		t.Fatalf("the install granted more than the identity's own storage it asked for: %+v", g)
	}

	// An update changes no grant: the operator's word on the card stands.
	if _, err := app.applyConfigChange(map[string]any{"plugins.grants." + id + ".memory": false}); err != nil {
		t.Fatal(err)
	}
	if err := app.InstallFromCatalog(context.Background(), id); err != nil {
		t.Fatalf("reinstall: %v", err)
	}
	if g := app.configSnapshot().Plugins.Grants[id]; g.Memory {
		t.Fatal("an install over an installed plugin regranted what the operator revoked")
	}
}
