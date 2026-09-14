package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/genesis"
	"github.com/aiii-dot-id/aii-os/internal/genesis/genesistest"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt/packagetest"
	"github.com/aiii-dot-id/aii-os/internal/sigenvelope"
)

// .
// .
// .
// .
// .
// .
// .
func TestARevocationSnapshotDeactivatesTheRunningRelease(t *testing.T) {
	dir := t.TempDir()
	result := genesistest.NewRoot(t).Birth(t, genesis.BirthConfig{
		Name:       "RevocationTest",
		KeyPath:    filepath.Join(dir, "identity.sec"),
		LedgerPath: filepath.Join(dir, "ledger.jsonl"),
		DBPath:     filepath.Join(dir, "aii.db"),
	})
	result.Ledger.Close()

	s, err := packagetest.NewSigner()
	if err != nil {
		t.Fatal(err)
	}
	pinRoot := func(name string, env *sigenvelope.PublicKeyEnvelope) string {
		raw, err := json.Marshal(env)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, raw, 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	certifierRoot := pinRoot("certifier-root.json", s.Certifier.Env)
	reviewerRoot := pinRoot("reviewer-root.json", s.Reviewer.Env)
	trustDir := filepath.Join(dir, "trust")
	if err := os.MkdirAll(trustDir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := s.MintEmptyStatus(trustDir); err != nil {
		t.Fatal(err)
	}

	// .
	// .
	const id = "org.example.revocable"
	wasm, err := os.ReadFile(filepath.Join("..", "pluginworker", "testdata", "responder.wasm"))
	if err != nil {
		t.Fatalf("read responder fixture: %v", err)
	}
	files := map[string][]byte{
		"interfaces/quarantine.probe.v1.schema.json": []byte(`{"interface":"quarantine.probe","v":1}`),
		"variants/linux-x86_64-wasm/plugin.wasm":     wasm,
	}
	manifest := packagetest.BuildManifestJSON(id, "0.1.0",
		[]packagetest.InterfaceSpec{{ID: "quarantine.probe", Version: 1, SchemaFile: "interfaces/quarantine.probe.v1.schema.json", Methods: []string{"ping"}}},
		[]packagetest.VariantSpec{{
			ID: "linux-x86_64-wasm", Platform: packagefmt.HostPlatform(), Arch: packagefmt.HostArch(),
			Topology: packagefmt.HostTopology(), Runtime: "wasm_component", Profile: "wasm_sandbox",
			Entrypoint: "variants/linux-x86_64-wasm/plugin.wasm",
		}},
		files, nil)
	spec := packagetest.PackageSpec{Root: id + "-0.1.0", Manifest: manifest, InstallFiles: files}
	if err := s.SignT1(&spec); err != nil {
		t.Fatal(err)
	}
	var sig sigenvelope.Envelope
	if err := json.Unmarshal(spec.Signatures[packagetest.SigFilePublisherSig], &sig); err != nil {
		t.Fatal(err)
	}
	pkgPath := filepath.Join(dir, id+"-0.1.0.aiiospkg")
	if err := os.WriteFile(pkgPath, packagetest.Build(spec), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)

	cfg := &Config{
		Identity: IdentityConfig{KeyPath: filepath.Join(dir, "identity.sec"),
			LedgerPath: filepath.Join(dir, "ledger.jsonl"), DBPath: filepath.Join(dir, "aii.db"),
		},
		LLM:        withTestProvider(t, dir, "test", "https://127.0.0.1:1", "m", "sk-x"),
		SourcePath: filepath.Join(dir, "config.json"),
		Dashboard:  DashboardConfig{Port: 0},
		Tools:      ToolsConfig{CWD: dir},
		Plugins:    PluginsConfig{Autoload: "T1", CertifierRoot: certifierRoot, ReviewerRoot: reviewerRoot},
		Agency:     defaultConfig().Agency,
	}
	app := New(cfg)
	if err := startLiveForTest(app); err != nil {
		t.Fatal(err)
	}
	defer app.Stop()
	installPluginDir(t, dir, id, pkgPath)

	const tool = "pl_org_example_revocable_ping"
	waitFor := func(what string, cond func() bool) {
		t.Helper()
		deadline := time.Now().Add(20 * time.Second)
		for !cond() {
			if time.Now().After(deadline) {
				t.Fatalf("timed out waiting for %s; tools: %v", what, app.toolReg.Names())
			}
			time.Sleep(200 * time.Millisecond)
		}
	}
	waitFor("the signed T1 release to activate", func() bool { _, ok := app.toolReg.Get(tool); return ok })

	// .
	// .
	raw, err := s.Certifier.SignRevocationStatus(2, []packagetest.RevocationEntry{{
		ArtifactKind: packagetest.ArtifactKindManifestSig, PayloadSHA256: sig.PayloadSHA256,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(trustDir, packagetest.StatusFileCertifier), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	waitFor("the revoked release to deactivate", func() bool { _, ok := app.toolReg.Get(tool); return !ok })
	if _, err := os.Stat(filepath.Join(dir, "plugins", id, filepath.Base(pkgPath))); err != nil {
		t.Fatalf("the package stays where it was; only the trust moved: %v", err)
	}
	if epoch, ok := app.pluginOpts.Roots.Revocation.Epoch(packagetest.KeyTypePublisherCertifier); !ok || epoch != 2 {
		t.Fatalf("the reloaded status set must carry the new epoch, got %d %v", epoch, ok)
	}
}
