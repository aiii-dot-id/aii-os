package app

// .
// .
// .
// .

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
	// .
	badDir := filepath.Join(dir, "plugins", "badplug")
	if err := os.MkdirAll(badDir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(badDir, "bad.aiiospkg"), []byte("not a package"), 0o644); err != nil {
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
		Plugins:    PluginsConfig{Autoload: "T0"},
		Agency:     defaultConfig().Agency,
	}
	app := New(cfg)
	if err := startLiveForTest(app); err != nil {
		t.Fatalf("a refused plugin package must never fail boot: %v", err)
	}
	defer app.Stop()

	// .
	const wantTool = "pl_org_example_good_ping"
	if _, ok := app.toolReg.Get(wantTool); !ok {
		t.Fatalf("good package's tool must be registered, names: %v", app.toolReg.Names())
	}
	if len(app.plugins) != 1 || app.plugins[0].ID != "org.example.good" {
		t.Fatalf("exactly the good package must be active: %+v", app.plugins)
	}

	// .
	res, err := app.toolReg.Execute(context.Background(), wantTool, map[string]interface{}{})
	if err != nil || res.Error != "" || !strings.Contains(res.Output, `"echoed":true`) {
		t.Fatalf("plugin tool must answer through the harness: %v %+v", err, res)
	}

	// .
	for _, name := range app.toolReg.Names() {
		if strings.HasPrefix(name, "pl_") && name != wantTool {
			t.Fatalf("the refused package must register nothing, found %s", name)
		}
	}
}

// .
// .
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

// .
// .
// .
// .
func TestPluginAutoloadThresholdAndInvariants(t *testing.T) {
	dir := t.TempDir()
	result := genesistest.NewRoot(t).Birth(t, genesis.BirthConfig{
		Name:       "PluginTest",
		KeyPath:    filepath.Join(dir, "identity.sec"),
		LedgerPath: filepath.Join(dir, "ledger.jsonl"),
		DBPath:     filepath.Join(dir, "aii.db"),
	})
	result.Ledger.Close()

	pkg := buildResponderPkg(t, dir, "org.example.gated")
	installPluginDir(t, dir, "org.example.gated", pkg)
	// .
	installPluginDir(t, dir, "copycat", pkg)
	// .
	installPluginDir(t, dir, "twins", pkg)
	installPluginDir(t, dir, "twins", buildResponderPkg(t, dir, "org.example.twin2"))
	// .
	if err := os.MkdirAll(filepath.Join(dir, "plugins", "empty"), 0o750); err != nil {
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
		// .
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

// .
// .
// .
func TestPluginDropInConvergesLive(t *testing.T) {
	dir := t.TempDir()
	result := genesistest.NewRoot(t).Birth(t, genesis.BirthConfig{
		Name:       "PluginTest",
		KeyPath:    filepath.Join(dir, "identity.sec"),
		LedgerPath: filepath.Join(dir, "ledger.jsonl"),
		DBPath:     filepath.Join(dir, "aii.db"),
	})
	result.Ledger.Close()
	// .
	// .
	pkg := buildResponderPkg(t, dir, "org.example.dropped")
	t.Chdir(dir)

	cfg := &Config{
		Identity: IdentityConfig{KeyPath: filepath.Join(dir, "identity.sec"),
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

	// .
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

	// .
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

// .
// .
// .
func buildResponderVer(t *testing.T, dir, id, ver, arch string) string {
	t.Helper()
	wasm, err := os.ReadFile(filepath.Join("..", "pluginworker", "testdata", "responder.wasm"))
	if err != nil {
		t.Fatalf("read responder fixture: %v", err)
	}
	if arch == "" {
		arch = packagefmt.HostArch()
	}
	files := map[string][]byte{
		"interfaces/quarantine.probe.v1.schema.json": []byte(`{"interface":"quarantine.probe","v":1}`),
		"variants/linux-x86_64-wasm/plugin.wasm":     wasm,
	}
	manifest := packagetest.BuildManifestJSON(id, ver,
		[]packagetest.InterfaceSpec{{
			ID: "quarantine.probe", Version: 1,
			SchemaFile: "interfaces/quarantine.probe.v1.schema.json",
			Methods:    []string{"ping"},
		}},
		[]packagetest.VariantSpec{{
			ID: "linux-x86_64-wasm", Platform: packagefmt.HostPlatform(), Arch: arch,
			Topology: packagefmt.HostTopology(), Runtime: "wasm_component", Profile: "wasm_sandbox",
			Entrypoint: "variants/linux-x86_64-wasm/plugin.wasm",
		}},
		files, nil)
	path := filepath.Join(dir, id+"-"+ver+".aiiospkg")
	if err := os.WriteFile(path, packagetest.Build(packagetest.PackageSpec{
		Root: id + "-" + ver, Manifest: manifest, InstallFiles: files,
	}), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// .
// .
func swapPackage(t *testing.T, installDir, id, oldPkg, newPkg string) {
	t.Helper()
	pdir := filepath.Join(installDir, "plugins", id)
	if err := os.Remove(filepath.Join(pdir, filepath.Base(oldPkg))); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(newPkg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pdir, filepath.Base(newPkg)), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func activeRelease(app *App, id string) (version string, count int) {
	app.pluginMu.Lock()
	defer app.pluginMu.Unlock()
	for _, p := range app.plugins {
		if p.ID == id {
			version = p.Version
		}
	}
	return version, len(app.plugins)
}

// .
// .
// .
func TestPluginUpdatesSideBySide(t *testing.T) {
	dir := t.TempDir()
	result := genesistest.NewRoot(t).Birth(t, genesis.BirthConfig{
		Name: "PluginTest", KeyPath: filepath.Join(dir, "identity.sec"),
		LedgerPath: filepath.Join(dir, "ledger.jsonl"), DBPath: filepath.Join(dir, "aii.db"),
	})
	result.Ledger.Close()
	v1 := buildResponderVer(t, dir, "org.example.up", "0.1.0", "")
	v2 := buildResponderVer(t, dir, "org.example.up", "0.2.0", "")
	installPluginDir(t, dir, "org.example.up", v1)
	t.Chdir(dir)

	cfg := &Config{
		Identity: IdentityConfig{KeyPath: filepath.Join(dir, "identity.sec"),
			LedgerPath: filepath.Join(dir, "ledger.jsonl"), DBPath: filepath.Join(dir, "aii.db")},
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

	const tool = "pl_org_example_up_ping"
	if _, ok := app.toolReg.Get(tool); !ok {
		t.Fatalf("the running release registered %s: %v", tool, app.toolReg.Names())
	}
	if v, n := activeRelease(app, "org.example.up"); v != "0.1.0" || n != 1 {
		t.Fatalf("the running release is 0.1.0 alone, got %q n=%d", v, n)
	}

	// .
	swapPackage(t, dir, "org.example.up", v1, v2)
	app.convergePlugins(context.Background())

	if _, ok := app.toolReg.Get(tool); !ok {
		t.Fatal("the tool name never leaves the registry across a side-by-side update")
	}
	if v, n := activeRelease(app, "org.example.up"); v != "0.2.0" || n != 1 {
		t.Fatalf("exactly the new release is active after the drain, got %q n=%d", v, n)
	}
	res, err := app.toolReg.Execute(context.Background(), tool, map[string]interface{}{})
	if err != nil || res.Error != "" || !strings.Contains(res.Output, `"echoed":true`) {
		t.Fatalf("the updated release answers through the same name: %v %+v", err, res)
	}
}

// .
// .
// .
func TestPluginUpdateRefusedKeepsRunning(t *testing.T) {
	dir := t.TempDir()
	result := genesistest.NewRoot(t).Birth(t, genesis.BirthConfig{
		Name: "PluginTest", KeyPath: filepath.Join(dir, "identity.sec"),
		LedgerPath: filepath.Join(dir, "ledger.jsonl"), DBPath: filepath.Join(dir, "aii.db"),
	})
	result.Ledger.Close()
	v1 := buildResponderVer(t, dir, "org.example.keep", "0.1.0", "")
	// .
	// .
	// .
	// .
	otherArch := "arm64"
	if packagefmt.HostArch() == "arm64" {
		otherArch = "x86_64"
	}
	bad := buildResponderVer(t, dir, "org.example.keep", "0.2.0", otherArch)
	installPluginDir(t, dir, "org.example.keep", v1)
	t.Chdir(dir)

	cfg := &Config{
		Identity: IdentityConfig{KeyPath: filepath.Join(dir, "identity.sec"),
			LedgerPath: filepath.Join(dir, "ledger.jsonl"), DBPath: filepath.Join(dir, "aii.db")},
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

	const tool = "pl_org_example_keep_ping"
	if _, ok := app.toolReg.Get(tool); !ok {
		t.Fatalf("the running release registered %s", tool)
	}

	swapPackage(t, dir, "org.example.keep", v1, bad)
	app.convergePlugins(context.Background())

	if v, n := activeRelease(app, "org.example.keep"); v != "0.1.0" || n != 1 {
		t.Fatalf("the running release keeps serving after a refused update, got %q n=%d", v, n)
	}
	res, err := app.toolReg.Execute(context.Background(), tool, map[string]interface{}{})
	if err != nil || res.Error != "" || !strings.Contains(res.Output, `"echoed":true`) {
		t.Fatalf("the running release still answers after the refused update: %v %+v", err, res)
	}
}
