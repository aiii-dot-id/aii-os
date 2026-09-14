package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/broker"
	"github.com/aiii-dot-id/aii-os/internal/genesis"
	"github.com/aiii-dot-id/aii-os/internal/genesis/genesistest"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt/packagetest"
	"github.com/aiii-dot-id/aii-os/internal/pluginworker/wasmgen"
)

// .
// .
// .
// .
// .
// .
func TestPluginSettingsFromTheViewToTheGuest(t *testing.T) {
	dir := t.TempDir()
	result := genesistest.NewRoot(t).Birth(t, genesis.BirthConfig{
		Name:       "SettingsTest",
		KeyPath:    filepath.Join(dir, "identity.sec"),
		LedgerPath: filepath.Join(dir, "ledger.jsonl"),
		DBPath:     filepath.Join(dir, "aii.db"),
	})
	result.Ledger.Close()

	const id = "org.example.configured"
	files := map[string][]byte{
		"interfaces/settings.probe.v1.schema.json": []byte(`[{"id":"probe.settings","summary":"Read my settings","effects":"read.internal","capabilities":[]}]`),
		"variants/linux-x86_64-wasm/plugin.wasm":   wasmgen.CannedCaller([]byte(`{"operation":"settings.get"}`)),
		"settings.json":                            []byte(`[{"key":"recall_limit","type":"number","title":"Recall limit","description":"How many texts a recall returns","default":5,"minimum":1,"maximum":50},{"key":"api_key","type":"secret","title":"API key","required":true}]`),
	}
	manifest := packagetest.BuildManifestJSON(id, "0.3.0",
		[]packagetest.InterfaceSpec{{ID: "settings.probe", Version: 1, SchemaFile: "interfaces/settings.probe.v1.schema.json", Methods: []string{"probe.settings"}}},
		[]packagetest.VariantSpec{{
			ID: "linux-x86_64-wasm", Platform: packagefmt.HostPlatform(), Arch: packagefmt.HostArch(),
			Topology: packagefmt.HostTopology(), Runtime: "wasm_component", Profile: "wasm_sandbox",
			Entrypoint: "variants/linux-x86_64-wasm/plugin.wasm",
		}},
		files, nil)
	pkg := filepath.Join(dir, id+"-0.3.0.aiiospkg")
	if err := os.WriteFile(pkg, packagetest.Build(packagetest.PackageSpec{Root: id + "-0.3.0", Manifest: manifest, InstallFiles: files}), 0o644); err != nil {
		t.Fatal(err)
	}
	installPluginDir(t, dir, id, pkg)
	t.Chdir(dir)

	cfg := &Config{
		Identity: IdentityConfig{KeyPath: filepath.Join(dir, "identity.sec"),
			LedgerPath: filepath.Join(dir, "ledger.jsonl"), DBPath: filepath.Join(dir, "aii.db"),
		},
		LLM:        withTestProvider(t, dir, "test", "https://127.0.0.1:1", "m", "sk-x"),
		SourcePath: filepath.Join(dir, "config.json"),
		Dashboard:  DashboardConfig{Port: 0},
		Tools:      ToolsConfig{CWD: dir},
		Plugins: PluginsConfig{Autoload: "T0",
			Grants:       map[string]broker.Grant{id: {CredentialHandles: []string{"acme"}}},
			AuthProfiles: map[string]broker.AuthProfile{"acme": {SecretEnv: "ACME_TOKEN", Host: "api.acme.test", Port: 443}},
			// .
			// .
			// .
			// .
			Settings: map[string]map[string]interface{}{id: {"legacy_top_k": 40.0}}},
		Agency: defaultConfig().Agency,
	}
	app := New(cfg)
	if err := startLiveForTest(app); err != nil {
		t.Fatal(err)
	}
	defer app.Stop()
	if len(app.plugins) != 1 {
		t.Fatalf("the configured plugin must be active: %+v", app.plugins)
	}
	tool := app.plugins[0].ToolNames[0]

	// .
	state, err := app.applyConfigChange(map[string]interface{}{})
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Plugins.Installed) != 1 || state.Plugins.Installed[0].Version != "0.3.0" || len(state.Plugins.Installed[0].Settings) != 3 {
		t.Fatalf("the view carries the declaration and what an earlier release left: %+v", state.Plugins.Installed)
	}
	// .
	// .
	// .
	if orphan := state.Plugins.Installed[0].Settings[2]; !orphan.Undeclared || orphan.Key != "legacy_top_k" || orphan.Value != 40.0 {
		t.Fatalf("the card shows what an earlier release left behind: %+v", orphan)
	}
	if s := state.Plugins.Installed[0].Settings[1]; s.Type != "secret" || len(s.Handles) != 1 || s.Handles[0] != "acme" || !s.Required {
		t.Fatalf("a secret setting offers the grant's handles: %+v", s)
	}

	// .
	res, _ := app.toolReg.Execute(context.Background(), tool, nil)
	if !strings.Contains(res.Output, `"values":{"recall_limit":5}`) {
		t.Fatalf("the declared default reaches the guest: %s %s", res.Output, res.Error)
	}

	// .
	for key, v := range map[string]interface{}{
		"plugins.settings." + id + ".recall_limit": 60.0,
		"plugins.settings." + id + ".colour":       "blue",
		"plugins.settings." + id + ".api_key":      "not-configured",
		"plugins.settings.org.example.absent.x":    1.0,
		"plugins.settings.malformed":               1.0,
	} {
		if _, err := app.applyConfigChange(map[string]interface{}{key: v}); err == nil {
			t.Fatalf("%s=%v must be refused", key, v)
		}
	}
	if got := app.configSnapshot().Plugins.Settings[id]; len(got) != 1 || got["legacy_top_k"] != 40.0 {
		t.Fatalf("a refused change stored something, or discarded what was already there: %v", got)
	}

	// .
	// .
	// .
	if _, err := app.applyConfigChange(map[string]interface{}{"plugins.settings." + id + ".legacy_top_k": 9.0}); err == nil ||
		!strings.Contains(err.Error(), "clear it by saving it empty") {
		t.Fatalf("an undeclared key cannot be SET, and the refusal says what can be done: %v", err)
	}
	state, err = app.applyConfigChange(map[string]interface{}{"plugins.settings." + id + ".legacy_top_k": nil})
	if err != nil {
		t.Fatalf("a value this release does not declare must be forgettable: %v", err)
	}
	if len(state.Plugins.Installed[0].Settings) != 2 || app.configSnapshot().Plugins.Settings[id] != nil {
		t.Fatalf("and then it is gone from both the card and the config: %+v %v", state.Plugins.Installed[0].Settings, app.configSnapshot().Plugins.Settings)
	}

	// .
	state, err = app.applyConfigChange(map[string]interface{}{
		"plugins.settings." + id + ".recall_limit": 8.0,
		"plugins.settings." + id + ".api_key":      "acme",
	})
	if err != nil {
		t.Fatal(err)
	}
	if s := state.Plugins.Installed[0].Settings; s[0].Value != 8.0 || s[1].Value != "acme" {
		t.Fatalf("the view shows the stored values: %+v", s)
	}
	res, _ = app.toolReg.Execute(context.Background(), tool, nil)
	if !strings.Contains(res.Output, `"values":{"api_key":"acme","recall_limit":8}`) {
		t.Fatalf("the operator's values reach the guest live: %s", res.Output)
	}

	// .
	if _, err := app.applyConfigChange(map[string]interface{}{
		"plugins.settings." + id + ".recall_limit": nil,
		"plugins.settings." + id + ".api_key":      nil,
	}); err != nil {
		t.Fatal(err)
	}
	res, _ = app.toolReg.Execute(context.Background(), tool, nil)
	if !strings.Contains(res.Output, `"values":{"recall_limit":5}`) {
		t.Fatalf("a cleared value returns the declared default: %s", res.Output)
	}
}

// .
// .
// .
// .
// .
// .
// .
// .
// .
func TestStoredSettingsSurviveRestartAndAreNamedWhenAReleaseRetiresThem(t *testing.T) {
	dir := t.TempDir()
	result := genesistest.NewRoot(t).Birth(t, genesis.BirthConfig{
		Name:       "RetireTest",
		KeyPath:    filepath.Join(dir, "identity.sec"),
		LedgerPath: filepath.Join(dir, "ledger.jsonl"),
		DBPath:     filepath.Join(dir, "aii.db"),
	})
	result.Ledger.Close()

	const id = "org.example.voiced"
	build := func(version, settings string) string {
		files := map[string][]byte{
			"interfaces/settings.probe.v1.schema.json": []byte(`[{"id":"probe.settings","summary":"Read my settings","effects":"read.internal","capabilities":[]}]`),
			"variants/linux-x86_64-wasm/plugin.wasm":   wasmgen.CannedCaller([]byte(`{"operation":"settings.get"}`)),
			"settings.json":                            []byte(settings),
		}
		manifest := packagetest.BuildManifestJSON(id, version,
			[]packagetest.InterfaceSpec{{ID: "settings.probe", Version: 1, SchemaFile: "interfaces/settings.probe.v1.schema.json", Methods: []string{"probe.settings"}}},
			[]packagetest.VariantSpec{{
				ID: "linux-x86_64-wasm", Platform: packagefmt.HostPlatform(), Arch: packagefmt.HostArch(),
				Topology: packagefmt.HostTopology(), Runtime: "wasm_component", Profile: "wasm_sandbox",
				Entrypoint: "variants/linux-x86_64-wasm/plugin.wasm",
			}},
			files, nil)
		pkg := filepath.Join(dir, id+"-"+version+".aiiospkg")
		if err := os.WriteFile(pkg, packagetest.Build(packagetest.PackageSpec{Root: id + "-" + version, Manifest: manifest, InstallFiles: files}), 0o644); err != nil {
			t.Fatal(err)
		}
		return pkg
	}
	first := build("0.3.0", `[{"key":"voice","type":"enum","title":"Speaking voice","values":["alba","ryan"],"labels":{"ryan":"Ryan (British English)"},"default":"alba"},{"key":"top_k","type":"integer","title":"Top-k","default":40,"minimum":1,"maximum":200}]`)
	installPluginDir(t, dir, id, first)
	t.Chdir(dir)
	llm := withTestProvider(t, dir, "test", "https://127.0.0.1:1", "m", "sk-x")
	newCfg := func() *Config {
		return &Config{
			Identity: IdentityConfig{KeyPath: filepath.Join(dir, "identity.sec"),
				LedgerPath: filepath.Join(dir, "ledger.jsonl"), DBPath: filepath.Join(dir, "aii.db"),
			},
			LLM:        llm,
			SourcePath: filepath.Join(dir, "config.json"),
			Dashboard:  DashboardConfig{Port: 0},
			Tools:      ToolsConfig{CWD: dir},
			Plugins:    PluginsConfig{Autoload: "T0"},
			Agency:     defaultConfig().Agency,
		}
	}
	app := New(newCfg())
	if err := startLiveForTest(app); err != nil {
		t.Fatal(err)
	}
	if len(app.plugins) != 1 {
		t.Fatalf("the plugin must be active: %+v", app.plugins)
	}
	key := func(setting string) string { return "plugins.settings." + id + "." + setting }

	state, err := app.applyConfigChange(map[string]interface{}{})
	if err != nil {
		t.Fatal(err)
	}
	pv := state.Plugins.Installed[0]
	if pv.Applies != "next_call" {
		t.Fatalf("a plugin the host invokes reads a save at its next call: %q", pv.Applies)
	}
	if v := pv.Settings[0]; v.Labels["ryan"] != "Ryan (British English)" || v.Effective != "alba" || v.Value != nil || v.Invalid != "" {
		t.Fatalf("the view carries the labels and the default in effect: %+v", v)
	}
	if k := pv.Settings[1]; k.Type != "integer" || k.Effective != 40.0 {
		t.Fatalf("an integer's default is in effect: %+v", k)
	}
	if _, err := app.applyConfigChange(map[string]interface{}{key("top_k"): 2.5}); err == nil || !strings.Contains(err.Error(), "whole number") {
		t.Fatalf("a fraction for an integer is refused by key before it is stored: %v", err)
	}
	state, err = app.applyConfigChange(map[string]interface{}{key("voice"): "ryan", key("top_k"): 3.0})
	if err != nil {
		t.Fatal(err)
	}
	if v := state.Plugins.Installed[0].Settings[0]; v.Value != "ryan" || v.Effective != "ryan" || v.Invalid != "" {
		t.Fatalf("a holding choice is stored, in effect, unflagged: %+v", v)
	}
	res, _ := app.toolReg.Execute(context.Background(), app.plugins[0].ToolNames[0], nil)
	if !strings.Contains(res.Output, `"values":{"top_k":3,"voice":"ryan"}`) {
		t.Fatalf("the choices reach the guest: %s %s", res.Output, res.Error)
	}
	onDisk, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil || !strings.Contains(string(onDisk), `"voice": "ryan"`) && !strings.Contains(string(onDisk), `"voice":"ryan"`) {
		t.Fatalf("the choice is persisted in config.json: %v %s", err, onDisk)
	}
	persisted := app.configSnapshot().Plugins.Settings
	app.Stop()

	// .
	// .
	if err := os.Remove(filepath.Join(dir, "plugins", id, filepath.Base(first))); err != nil {
		t.Fatal(err)
	}
	installPluginDir(t, dir, id, build("0.4.0", `[{"key":"voice","type":"enum","title":"Speaking voice","values":["alba"],"default":"alba"},{"key":"top_k","type":"integer","title":"Top-k","default":1,"minimum":1,"maximum":2}]`))
	cfg2 := newCfg()
	cfg2.Plugins.Settings = persisted
	app2 := New(cfg2)
	if err := startLiveForTest(app2); err != nil {
		t.Fatal(err)
	}
	defer app2.Stop()
	state, err = app2.applyConfigChange(map[string]interface{}{})
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Plugins.Installed) != 1 {
		t.Fatalf("the new release must be active after the restart: installed=%+v skips=%+v plugins=%d", state.Plugins.Installed, state.Plugins.Skips, len(app2.plugins))
	}
	pv = state.Plugins.Installed[0]
	if pv.Version != "0.4.0" {
		t.Fatalf("the new release is active: %+v", pv)
	}
	if v := pv.Settings[0]; v.Value != "ryan" || v.Effective != "alba" || !strings.Contains(v.Invalid, "must be one of") {
		t.Fatalf("a retired choice is shown as stored, named invalid, the default in effect: %+v", v)
	}
	if k := pv.Settings[1]; k.Value != 3.0 || k.Effective != 1.0 || !strings.Contains(k.Invalid, "above the maximum") {
		t.Fatalf("a value over the new ceiling is shown as stored, named, the default in effect: %+v", k)
	}
	res, _ = app2.toolReg.Execute(context.Background(), app2.plugins[0].ToolNames[0], nil)
	if !strings.Contains(res.Output, `"values":{"top_k":1,"voice":"alba"}`) {
		t.Fatalf("stale values are never handed over: %s %s", res.Output, res.Error)
	}
	if _, err := app2.applyConfigChange(map[string]interface{}{key("voice"): "ryan"}); err == nil || !strings.Contains(err.Error(), "must be one of") {
		t.Fatalf("the stale choice saved unchanged is refused by name: %v", err)
	}
	if got := app2.configSnapshot().Plugins.Settings[id]["voice"]; got != "ryan" {
		t.Fatalf("a refused save leaves the stored choice as it was, for the operator to see: %v", got)
	}
	state, err = app2.applyConfigChange(map[string]interface{}{key("voice"): "alba", key("top_k"): 2.0})
	if err != nil {
		t.Fatal(err)
	}
	if v, k := state.Plugins.Installed[0].Settings[0], state.Plugins.Installed[0].Settings[1]; v.Invalid != "" || k.Invalid != "" || v.Effective != "alba" || k.Effective != 2.0 {
		t.Fatalf("choosing again clears the flags: %+v %+v", v, k)
	}
}
