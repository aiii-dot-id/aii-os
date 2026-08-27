package app

// Quarantine-harness wiring: config plugins.packages activates each
// listed package at startLive; a refused package is skipped with its
// reason logged and the runtime boots unaffected (fail-closed PER
// PLUGIN, never fail-boot).

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/genesis"
	"github.com/aiii-dot-id/aii-os/internal/genesis/genesistest"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt/packagetest"
)

func buildResponderPkg(t *testing.T, dir, id string) string {
	t.Helper()
	wasm, err := os.ReadFile(filepath.Join("..", "pluginworker", "testdata", "responder.wasm"))
	if err != nil {
		t.Fatalf("read responder fixture: %v (run `go generate ./internal/pluginworker`)", err)
	}
	files := map[string][]byte{
		"interfaces/quarantine.probe.v1.schema.json": []byte(`{"interface":"quarantine.probe","v":1}`),
		"variants/linux-x86_64-wasm/plugin.wasm":     wasm,
	}
	manifest := packagetest.BuildManifestJSON(id, "0.1.0",
		[]packagetest.InterfaceSpec{{
			ID: "quarantine.probe", Version: 1,
			SchemaFile: "interfaces/quarantine.probe.v1.schema.json",
			Methods:    []string{"ping"},
		}},
		[]packagetest.VariantSpec{{
			ID: "linux-x86_64-wasm", Platform: packagefmt.HostPlatform(), Arch: packagefmt.HostArch(),
			Topology: packagefmt.HostTopology(), Runtime: "wasm_component", Profile: "wasm_sandbox",
			Entrypoint: "variants/linux-x86_64-wasm/plugin.wasm",
		}},
		files, nil)
	path := filepath.Join(dir, id+"-0.1.0.aiiospkg")
	if err := os.WriteFile(path, packagetest.Build(packagetest.PackageSpec{
		Root: id + "-0.1.0", Manifest: manifest, InstallFiles: files,
	}), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestPluginPackagesActivateAtStartup(t *testing.T) {
	dir := t.TempDir()
	result := genesistest.NewRoot(t).Birth(t, genesis.BirthConfig{
		Name:       "PluginTest",
		KeyPath:    filepath.Join(dir, "identity.sec"),
		LedgerPath: filepath.Join(dir, "ledger.jsonl"),
		DBPath:     filepath.Join(dir, "aii.db"),
	})
	result.Ledger.Close()

	good := buildResponderPkg(t, dir, "org.example.good")
	installPluginDir(t, dir, "org.example.good", good)
	// Garbage evidence: refused at EVERY autoload level (not T0).
	badDir := filepath.Join(dir, "plugins", "badplug")
	if err := os.MkdirAll(badDir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(badDir, "bad.aiiospkg"), []byte("not a package"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir) // plugins/ resolves against the install dir (entirely local)

	cfg := &Config{
		Identity: IdentityConfig{
			Name: "PluginTest", KeyPath: filepath.Join(dir, "identity.sec"),
			LedgerPath: filepath.Join(dir, "ledger.jsonl"), DBPath: filepath.Join(dir, "aii.db"),
		},
		LLM:        withTestProvider(t, dir, "test", "https://127.0.0.1:1", "m", "sk-x"),
		SourcePath: filepath.Join(dir, "config.json"),
		Dashboard:  DashboardConfig{Port: 0},
		Tools:      ToolsConfig{CWD: dir},
		Plugins:    PluginsConfig{Autoload: "T0"}, // dev posture: unsigned T0 loads; garbage still refuses
		Agency:     defaultConfig().Agency,
	}
	app := New(cfg)
	if err := startLiveForTest(app); err != nil {
		t.Fatalf("a refused plugin package must never fail boot: %v", err)
	}
	defer app.Stop()

	// The good package's signed operation is an identity-callable tool.
	const wantTool = "pl_org_example_good_ping"
	if _, ok := app.toolReg.Get(wantTool); !ok {
		t.Fatalf("good package's tool must be registered, names: %v", app.toolReg.Names())
	}
	if len(app.plugins) != 1 || app.plugins[0].ID != "org.example.good" {
		t.Fatalf("exactly the good package must be active: %+v", app.plugins)
	}

	// It runs through the SAME dispatch path the identity uses.
	res, err := app.toolReg.Execute(context.Background(), wantTool, map[string]interface{}{})
	if err != nil || res.Error != "" || !strings.Contains(res.Output, `"echoed":true`) {
		t.Fatalf("plugin tool must answer through the harness: %v %+v", err, res)
	}

	// The bad package left no trace in the registry.
	for _, name := range app.toolReg.Names() {
		if strings.HasPrefix(name, "pl_") && name != wantTool {
			t.Fatalf("the refused package must register nothing, found %s", name)
		}
	}
}

// installPluginDir places a built package into the plugins/ registry
// layout (one directory per plugin) under the install dir.
func installPluginDir(t *testing.T, installDir, plugDirName, pkgPath string) {
	t.Helper()
	d := filepath.Join(installDir, "plugins", plugDirName)
	if err := os.MkdirAll(d, 0o750); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(pkgPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, filepath.Base(pkgPath)), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// The autoload gate: a present, VERIFIED T0 package below the default
// threshold (T1) is discovered, surfaced, and NOT loaded; duplicates of
// one verified id refuse; two packages in one directory refuse; an
// empty plugin directory is skipped.
func TestPluginAutoloadThresholdAndInvariants(t *testing.T) {
	dir := t.TempDir()
	result := genesistest.NewRoot(t).Birth(t, genesis.BirthConfig{
		Name:       "GateTest",
		KeyPath:    filepath.Join(dir, "identity.sec"),
		LedgerPath: filepath.Join(dir, "ledger.jsonl"),
		DBPath:     filepath.Join(dir, "aii.db"),
	})
	result.Ledger.Close()

	pkg := buildResponderPkg(t, dir, "org.example.gated")
	installPluginDir(t, dir, "org.example.gated", pkg)
	// Duplicate verified id in a second, differently named dir.
	installPluginDir(t, dir, "copycat", pkg)
	// Ambiguous dir: two packages.
	installPluginDir(t, dir, "twins", pkg)
	installPluginDir(t, dir, "twins", buildResponderPkg(t, dir, "org.example.twin2"))
	// Empty dir.
	if err := os.MkdirAll(filepath.Join(dir, "plugins", "empty"), 0o750); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)

	cfg := &Config{
		Identity: IdentityConfig{
			Name: "GateTest", KeyPath: filepath.Join(dir, "identity.sec"),
			LedgerPath: filepath.Join(dir, "ledger.jsonl"), DBPath: filepath.Join(dir, "aii.db"),
		},
		LLM:        withTestProvider(t, dir, "test", "https://127.0.0.1:1", "m", "sk-x"),
		SourcePath: filepath.Join(dir, "config.json"),
		Dashboard:  DashboardConfig{Port: 0},
		Tools:      ToolsConfig{CWD: dir},
		// No Autoload set: applyDefaults would set T1; New(cfg) path — set explicitly.
		Plugins: PluginsConfig{Autoload: "T1"},
		Agency:  defaultConfig().Agency,
	}
	app := New(cfg)
	if err := startLiveForTest(app); err != nil {
		t.Fatalf("boot must survive every refused/skipped plugin: %v", err)
	}
	defer app.Stop()

	if len(app.plugins) != 0 {
		t.Fatalf("T0 packages must not load under autoload T1, active: %+v", app.plugins)
	}
	var gated bool
	for _, sk := range app.pluginSkips {
		if sk.ID == "org.example.gated" {
			gated = true
			if sk.Tier != "T0" || sk.Reason == "" {
				t.Fatalf("skip must carry verified tier and an honest reason: %+v", sk)
			}
		}
	}
	if !gated {
		t.Fatalf("the below-threshold package must be SURFACED, skips: %+v", app.pluginSkips)
	}
}

// The operator's requirement (2026-08-20): drop-in time, not boot. A
// directory dropped into plugins/ while the runtime is LIVE verifies
// and activates within the sweep interval; removing it deactivates.
func TestPluginDropInConvergesLive(t *testing.T) {
	dir := t.TempDir()
	result := genesistest.NewRoot(t).Birth(t, genesis.BirthConfig{
		Name:       "DropIn",
		KeyPath:    filepath.Join(dir, "identity.sec"),
		LedgerPath: filepath.Join(dir, "ledger.jsonl"),
		DBPath:     filepath.Join(dir, "aii.db"),
	})
	result.Ledger.Close()
	// Build the fixture BEFORE chdir: the responder.wasm lookup is
	// relative to the repo, and after startLive the cwd is the install dir.
	pkg := buildResponderPkg(t, dir, "org.example.dropped")
	t.Chdir(dir)

	cfg := &Config{
		Identity: IdentityConfig{
			Name: "DropIn", KeyPath: filepath.Join(dir, "identity.sec"),
			LedgerPath: filepath.Join(dir, "ledger.jsonl"), DBPath: filepath.Join(dir, "aii.db"),
		},
		LLM:        withTestProvider(t, dir, "test", "https://127.0.0.1:1", "m", "sk-x"),
		SourcePath: filepath.Join(dir, "config.json"),
		Dashboard:  DashboardConfig{Port: 0},
		Tools:      ToolsConfig{CWD: dir},
		Plugins:    PluginsConfig{Autoload: "T0"},
		Agency:     defaultConfig().Agency,
	}
	app := New(cfg)
	if err := startLiveForTest(app); err != nil {
		t.Fatal(err)
	}
	defer app.Stop()
	if len(app.plugins) != 0 {
		t.Fatal("empty plugins/ must start empty")
	}

	// THE DROP: while live.
	installPluginDir(t, dir, "org.example.dropped", pkg)

	const tool = "pl_org_example_dropped_ping"
	deadline := time.Now().Add(20 * time.Second)
	for {
		if _, ok := app.toolReg.Get(tool); ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("dropped plugin did not activate within the sweep window; tools: %v", app.toolReg.Names())
		}
		time.Sleep(200 * time.Millisecond)
	}

	// THE REMOVAL: rm the directory = uninstall, live.
	if err := os.RemoveAll(filepath.Join(dir, "plugins", "org.example.dropped")); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(20 * time.Second)
	for {
		app.pluginMu.Lock()
		n := len(app.plugins)
		app.pluginMu.Unlock()
		if n == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("removed plugin did not deactivate within the sweep window: %d active", n)
		}
		time.Sleep(200 * time.Millisecond)
	}
}
