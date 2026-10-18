package app

// Quarantine-harness wiring: config plugins.packages activates each
// listed package at startLive; a refused package is skipped with its
// reason logged and the runtime boots unaffected (fail-closed PER
// PLUGIN, never fail-boot).

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/atomicfile"
	"github.com/aiii-dot-id/aii-os/internal/genesis"
	"github.com/aiii-dot-id/aii-os/internal/genesis/genesistest"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt/packagetest"
	"github.com/aiii-dot-id/aii-os/internal/pluginfacility"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
	"github.com/aiii-dot-id/aii-os/internal/tools"
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
		Identity: IdentityConfig{KeyPath: filepath.Join(dir, "identity.sec"),
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
	defer stopApp(t, app)

	// The good package's signed operation is an identity-callable tool.
	const wantTool = "pl_org_example_good_ping"
	if _, ok := app.toolReg.Get(wantTool); !ok {
		t.Fatalf("good package's tool must be registered, names: %v", app.toolReg.Names())
	}
	if len(app.plugins) != 1 || app.plugins[0].ID != "org.example.good" {
		t.Fatalf("exactly the good package must be active: %+v", app.plugins)
	}

	// It runs through the SAME dispatch path the identity uses.
	res, err := app.toolReg.Execute(context.Background(), wantTool, map[string]any{})
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
		Name:       "PluginTest",
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
		Identity: IdentityConfig{KeyPath: filepath.Join(dir, "identity.sec"),
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
	defer stopApp(t, app)

	if len(app.plugins) != 0 {
		t.Fatalf("T0 packages must not load under autoload T1, active: %+v", app.plugins)
	}
	var gated bool
	skips := app.pluginSkipViews()
	for _, sk := range skips {
		if sk.ID == "org.example.gated" {
			gated = true
			if sk.Tier != "T0" || sk.Reason == "" {
				t.Fatalf("skip must carry verified tier and an honest reason: %+v", sk)
			}
		}
	}
	if !gated {
		t.Fatalf("the below-threshold package must be SURFACED, skips: %+v", skips)
	}
}

func TestPluginDropInConvergesLive(t *testing.T) {
	dir := t.TempDir()
	result := genesistest.NewRoot(t).Birth(t, genesis.BirthConfig{
		Name:       "PluginTest",
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
	defer stopApp(t, app)
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

// buildResponderVer builds a responder package at a chosen version, on
// an arch the caller names — the host's when empty, a bogus one to make
// the only variant unselectable (S12 update fixtures).
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

// swapPackage publishes a complete replacement over the one visible package.
// Deleting first is an uninstall; writing the visible file in place exposes a
// partial package to the real watcher. Neither models a side-by-side update.
// Version belongs to the signed manifest, not this retained filename.
func swapPackage(t *testing.T, installDir, id, newPkg string) {
	t.Helper()
	if err := replaceTestPackage(filepath.Join(installDir, "plugins", id), newPkg); err != nil {
		t.Fatal(err)
	}
}

func replaceTestPackage(dir, newPkg string) error {
	current, err := filepath.Glob(filepath.Join(dir, "*.aiiospkg"))
	if err != nil {
		return err
	}
	if len(current) != 1 {
		return fmt.Errorf("update fixture needs one installed package, found %d", len(current))
	}
	data, err := os.ReadFile(newPkg)
	if err != nil {
		return err
	}
	// Published whole or not at all, as the update itself is.
	published, err := atomicfile.WriteReplace(current[0], data, 0o600)
	if err != nil && published {
		return fmt.Errorf("%s published but not durable: %w", filepath.Base(current[0]), err)
	}
	return err
}

func TestPackageUpdateFixtureRetainsTheOldPackageUntilReplacementIsReady(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "old.aiiospkg")
	if err := os.WriteFile(path, []byte("old whole package"), 0600); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(t.TempDir(), "missing.aiiospkg")
	if err := replaceTestPackage(dir, missing); err == nil {
		t.Fatal("missing replacement was accepted")
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != "old whole package" {
		t.Fatalf("a failed load withdrew the old package: %q %v", got, err)
	}
	next := filepath.Join(t.TempDir(), "new.aiiospkg")
	if err := os.WriteFile(next, []byte("new whole package"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := replaceTestPackage(dir, next); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 || entries[0].Name() != "old.aiiospkg" {
		t.Fatalf("replacement changed the visible package set: %v %v", entries, err)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != "new whole package" {
		t.Fatalf("replacement incomplete: %q %v", got, err)
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

// A new release of a running plugin lands SIDE BY SIDE (S12): the tool
// name never leaves the registry, exactly one release is active at the
// end, and it is the new one — the predecessor drained and stopped.
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
	defer stopApp(t, app)

	const tool = "pl_org_example_up_ping"
	if _, ok := app.toolReg.Get(tool); !ok {
		t.Fatalf("the running release registered %s: %v", tool, app.toolReg.Names())
	}
	if v, n := activeRelease(app, "org.example.up"); v != "0.1.0" || n != 1 {
		t.Fatalf("the running release is 0.1.0 alone, got %q n=%d", v, n)
	}

	// THE UPDATE, live: replace the dir's package and converge.
	swapPackage(t, dir, "org.example.up", v2)
	app.rescanPlugins(context.Background())

	// The pass asked for the update and returned; the candidate stands
	// up behind it. Through all of that the tool name must never leave
	// the registry — the old release answers until the redirect.
	if _, ok := app.toolReg.Get(tool); !ok {
		t.Fatal("the tool name never leaves the registry across a side-by-side update")
	}
	awaitPluginVersion(t, app, "org.example.up", "0.2.0")
	if _, ok := app.toolReg.Get(tool); !ok {
		t.Fatal("nor after it")
	}
	if v, n := activeRelease(app, "org.example.up"); v != "0.2.0" || n != 1 {
		t.Fatalf("exactly the new release is active after the drain, got %q n=%d", v, n)
	}
	res, err := app.toolReg.Execute(context.Background(), tool, map[string]any{})
	if err != nil || res.Error != "" || !strings.Contains(res.Output, `"echoed":true`) {
		t.Fatalf("the updated release answers through the same name: %v %+v", err, res)
	}
}

// A candidate that cannot stand up on this host is REFUSED, not landed:
// the running release keeps serving, unchanged — an update is never a
// gap (S12 rollback).
func TestPluginUpdateRefusedKeepsRunning(t *testing.T) {
	dir := t.TempDir()
	result := genesistest.NewRoot(t).Birth(t, genesis.BirthConfig{
		Name: "PluginTest", KeyPath: filepath.Join(dir, "identity.sec"),
		LedgerPath: filepath.Join(dir, "ledger.jsonl"), DBPath: filepath.Join(dir, "aii.db"),
	})
	result.Ledger.Close()
	v1 := buildResponderVer(t, dir, "org.example.keep", "0.1.0", "")
	// A supported arch that is NOT this host's: the package VERIFIES
	// (the manifest is valid) but its only variant never SELECTS here,
	// so the candidate cannot stand up — the refused-update path, not a
	// garbage package that would fail verification outright.
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
	defer stopApp(t, app)

	const tool = "pl_org_example_keep_ping"
	if _, ok := app.toolReg.Get(tool); !ok {
		t.Fatalf("the running release registered %s", tool)
	}

	swapPackage(t, dir, "org.example.keep", bad)
	app.rescanPlugins(context.Background())

	if v, n := activeRelease(app, "org.example.keep"); v != "0.1.0" || n != 1 {
		t.Fatalf("the running release keeps serving after a refused update, got %q n=%d", v, n)
	}
	res, err := app.toolReg.Execute(context.Background(), tool, map[string]any{})
	if err != nil || res.Error != "" || !strings.Contains(res.Output, `"echoed":true`) {
		t.Fatalf("the running release still answers after the refused update: %v %+v", err, res)
	}
}

// buildContractVer builds a responder release whose one operation
// declares its input schema at schemas/in.json; a nil input leaves that
// file out of the package, so the declaration names nothing.
func buildContractVer(t *testing.T, dir, id, ver string, input []byte) string {
	t.Helper()
	wasm, err := os.ReadFile(filepath.Join("..", "pluginworker", "testdata", "responder.wasm"))
	if err != nil {
		t.Fatalf("read responder fixture: %v", err)
	}
	files := map[string][]byte{
		"interfaces/quarantine.probe.v1.schema.json": []byte(`[{"id":"ping","input":"schemas/in.json"}]`),
		"variants/linux-x86_64-wasm/plugin.wasm":     wasm,
	}
	if input != nil {
		files["schemas/in.json"] = input
	}
	manifest := packagetest.BuildManifestJSON(id, ver,
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
	path := filepath.Join(dir, id+"-"+ver+".aiiospkg")
	if err := os.WriteFile(path, packagetest.Build(packagetest.PackageSpec{
		Root: id + "-" + ver, Manifest: manifest, InstallFiles: files,
	}), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestPluginUpdateWithAnUnhonorableContractKeepsRunning(t *testing.T) {
	dir := t.TempDir()
	result := genesistest.NewRoot(t).Birth(t, genesis.BirthConfig{
		Name: "PluginTest", KeyPath: filepath.Join(dir, "identity.sec"),
		LedgerPath: filepath.Join(dir, "ledger.jsonl"), DBPath: filepath.Join(dir, "aii.db"),
	})
	result.Ledger.Close()
	const id = "org.example.contract"
	strict := []byte(`{"type":"object","properties":{"id":{"type":"integer","minimum":1}},"required":["id"],"additionalProperties":false}`)
	v1 := buildContractVer(t, dir, id, "0.1.0", strict)
	bad := buildContractVer(t, dir, id, "0.2.0", nil)
	fixed := buildContractVer(t, dir, id, "0.2.1", strict)
	installPluginDir(t, dir, id, v1)
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
	defer stopApp(t, app)

	const tool = "pl_org_example_contract_ping"
	call := func(args map[string]any) tools.Result {
		t.Helper()
		res, err := app.toolReg.Execute(context.Background(), tool, args)
		if err != nil {
			t.Fatalf("%s: %v", tool, err)
		}
		return res
	}
	if res := call(map[string]any{"id": 2}); res.Error != "" || !strings.Contains(res.Output, `"echoed":true`) {
		t.Fatalf("the running release answers: %+v", res)
	}

	swapPackage(t, dir, id, bad)
	app.rescanPlugins(context.Background())
	var refusal *pluginfacility.Refusal
	var view pluginfacility.InstanceView
	deadline := time.Now().Add(30 * time.Second)
	for refusal == nil {
		if v, _ := activeRelease(app, id); v == "0.2.0" {
			t.Fatal("the candidate was admitted without the contract it declared")
		}
		for _, v := range app.pluginFacility().Snapshot().Instances {
			if v.ID == id && v.Refusal != nil && v.Refusal.Version == "0.2.0" {
				refusal, view = v.Refusal, v
			}
		}
		if refusal == nil {
			if time.Now().After(deadline) {
				t.Fatal("timed out waiting for the candidate's refusal")
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	var se *pluginhost.SchemaError
	if refusal.Stage != pluginfacility.StageStart || !errors.As(refusal.Cause, &se) || se.Operation != "ping" || se.File != "schemas/in.json" {
		t.Fatalf("the candidate is refused at start naming ping and schemas/in.json, got %s: %v", refusal.Stage, refusal.Cause)
	}
	if v, n := activeRelease(app, id); v != "0.1.0" || n != 1 {
		t.Fatalf("the running release keeps serving after the refused update, got %q n=%d", v, n)
	}
	if res := call(map[string]any{"id": 2}); res.Error != "" || !strings.Contains(res.Output, `"echoed":true`) {
		t.Fatalf("the running release still answers: %+v", res)
	}
	if res := call(map[string]any{"id": "x"}); !strings.Contains(res.Error, "arguments refused before the call") {
		t.Fatalf("the running release's own contract still holds: %+v", res)
	}

	// The same bytes are not asked again on a clock: the refusal waits on
	// the package, and a corrected release is a new question answered at
	// once.
	if refusal.Class != pluginfacility.ClassPermanent || !refusal.WaitsOn(pluginfacility.InputPackage) || !view.RetryAt.IsZero() {
		t.Fatalf("the refused candidate waits for different bytes, not a clock: class %s, wake %v, retry at %v", refusal.Class, refusal.WakeOn, view.RetryAt)
	}
	swapPackage(t, dir, id, fixed)
	app.rescanPlugins(context.Background())
	awaitPluginVersion(t, app, id, "0.2.1")
	if res := call(map[string]any{"id": "x"}); !strings.Contains(res.Error, "arguments refused before the call") {
		t.Fatalf("the corrected release serves under its contract: %+v", res)
	}
}

// THE PASS DECIDES; IT DOES NOT WAIT. This is the property the whole
// facility exists for. Activation used to happen inside convergePlugins,
// serially, on the sweep's own goroutine — so the boot pass held the
// identity until every plugin had verified its files, fetched what it
// needed and stood its child up, before the dashboard existed. Two
// native engines were a minute of an identity that could not answer.
//
// The pass now hands what it found to the facility and returns. The
// activations land behind it, one executor per id, beside each other.
func TestTheConvergePassHandsOverAndReturnsWithoutWaiting(t *testing.T) {
	dir := t.TempDir()
	result := genesistest.NewRoot(t).Birth(t, genesis.BirthConfig{
		Name: "PassTest", KeyPath: filepath.Join(dir, "identity.sec"),
		LedgerPath: filepath.Join(dir, "ledger.jsonl"), DBPath: filepath.Join(dir, "aii.db"),
	})
	result.Ledger.Close()
	ids := []string{"org.example.one", "org.example.two", "org.example.three"}
	// Built BEFORE the chdir: the fixture path is relative to the
	// package directory, not the identity's.
	built := map[string]string{}
	for _, id := range ids {
		built[id] = buildResponderPkg(t, dir, id)
		installPluginDir(t, dir, id, built[id])
	}
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
	defer stopApp(t, app)
	awaitPlugins(t, app)

	// Take them all away and put them back, then time the pass itself.
	// It must not be waiting for three activations to complete.
	for _, id := range ids {
		if err := os.RemoveAll(filepath.Join(dir, "plugins", id)); err != nil {
			t.Fatal(err)
		}
	}
	app.rescanPlugins(context.Background())
	awaitPlugins(t, app)
	for _, id := range ids {
		installPluginDir(t, dir, id, built[id])
	}

	start := time.Now()
	app.rescanPlugins(context.Background())
	elapsed := time.Since(start)

	// The pass returned. Whether the plugins are up yet is the
	// facility's business, and the identity is already answering.
	awaitPlugins(t, app)
	app.pluginMu.Lock()
	active := len(app.plugins)
	app.pluginMu.Unlock()
	if active != 3 {
		t.Fatalf("all three must come up behind the pass, got %d — %s", active, whyNotActive(app))
	}
	// A serial pass would have carried three whole activations. The
	// bound is deliberately far above anything the pass itself can
	// cost — it verifies three small packages — so that a loaded gate
	// box under the race detector cannot flake it. It is generous on
	// purpose: this is not a benchmark, and with
	// in-process wasm plugins a serial pass would not blow it either —
	// what it guards is a NATIVE engine ever being started inside the
	// pass again, which is tens of seconds each. That the activations
	// actually run beside one another is proved where it can be proved
	// exactly, in pluginfacility: TestDifferentInstancesStartConcurrently
	// puts four of them inside Start at the same instant.
	if elapsed > 5*time.Second {
		t.Fatalf("the pass did the work itself instead of handing it over: %s", elapsed)
	}
}

func TestAGrantIsOrphanedOnlyWhenNothingWantsIt(t *testing.T) {
	wanted := map[string]bool{"org.example.starting": true, "org.example.serving": true}
	granted := []string{"org.example.serving", "org.example.starting", "org.example.uninstalled", "org.example.below"}
	got := orphanedGrants(granted, wanted)
	want := []string{"org.example.below", "org.example.uninstalled"}
	if len(got) != len(want) {
		t.Fatalf("orphaned = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("orphaned = %v, want %v", got, want)
		}
	}
	// Nothing wanted at all: every grant is orphaned, and in a stable
	// order so the log does not shuffle between passes.
	if all := orphanedGrants(granted, nil); len(all) != 4 || all[0] != "org.example.below" {
		t.Fatalf("with nothing wanted every grant is orphaned, in order: %v", all)
	}
	// And a host that grants nothing says nothing.
	if none := orphanedGrants(nil, wanted); none != nil {
		t.Fatalf("no grants, no lines: %v", none)
	}
}
