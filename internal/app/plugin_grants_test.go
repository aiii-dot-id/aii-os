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
func TestPluginGrantsFromThePageToTheFile(t *testing.T) {
	const id = "org.example.memory"
	cfg := defaultConfig()
	cfg.SourcePath = filepath.Join(t.TempDir(), "config.json")
	cfg.Plugins.Grants = map[string]broker.Grant{id: {KV: true, Hosts: []string{"api.example.test:443"}}}
	if _, err := saveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	a := New(cfg)
	live := a.configSnapshot().Plugins.Grants

	// .
	// .
	for key, v := range map[string]interface{}{
		"plugins.grants." + id + ".memory": "yes",
		"plugins.grants." + id + ".hosts":  true,
		"plugins.grants." + id + ".roots":  false,
		"plugins.grants." + id + ".":       true,
		"plugins.grants.memory":            true,
		"plugins.grants." + id + ".colour": true,
	} {
		if _, err := a.applyConfigChange(map[string]interface{}{key: v, "plugins.grants." + id + ".tools": true}); err == nil {
			t.Fatalf("%s=%v must be refused", key, v)
		}
	}
	if g := a.configSnapshot().Plugins.Grants[id]; g.Tools || g.Memory || !g.KV {
		t.Fatalf("a refused change stored something: %+v", g)
	}

	// .
	// .
	if _, err := a.applyConfigChange(map[string]interface{}{
		"plugins.grants." + id + ".memory":     true,
		"plugins.grants." + id + ".embeddings": true,
	}); err != nil {
		t.Fatal(err)
	}
	got := a.configSnapshot().Plugins.Grants[id]
	if !got.KV || !got.Memory || !got.Embeddings || got.Tools || len(got.Hosts) != 1 {
		t.Fatalf("live grant = %+v", got)
	}
	if was := live[id]; was.Memory || was.Embeddings {
		t.Fatal("the candidate wrote through the live map before publication")
	}
	onDisk, err := LoadConfig(cfg.SourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if d := onDisk.Plugins.Grants[id]; !d.KV || !d.Memory || !d.Embeddings || len(d.Hosts) != 1 || d.Hosts[0] != "api.example.test:443" {
		t.Fatalf("published grant = %+v", d)
	}

	// .
	if _, err := a.applyConfigChange(map[string]interface{}{
		"plugins.grants." + id + ".kv":         false,
		"plugins.grants." + id + ".memory":     false,
		"plugins.grants." + id + ".embeddings": false,
	}); err != nil {
		t.Fatal(err)
	}
	if g, ok := a.configSnapshot().Plugins.Grants[id]; !ok || g.KV || g.Memory || g.Embeddings || len(g.Hosts) != 1 {
		t.Fatalf("a grant with a host list stays listed: %+v (%v)", g, ok)
	}
	// .
	const other = "org.example.other"
	if _, err := a.applyConfigChange(map[string]interface{}{"plugins.grants." + other + ".voice": true}); err != nil {
		t.Fatal(err)
	}
	if g := a.configSnapshot().Plugins.Grants[other]; !g.Voice {
		t.Fatalf("the second plugin's grant = %+v", g)
	}
	if _, err := a.applyConfigChange(map[string]interface{}{"plugins.grants." + other + ".voice": false}); err != nil {
		t.Fatal(err)
	}
	if _, ok := a.configSnapshot().Plugins.Grants[other]; ok {
		t.Fatal("an entry granting nothing must be removed")
	}
	onDisk, err = LoadConfig(cfg.SourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := onDisk.Plugins.Grants[other]; ok || len(onDisk.Plugins.Grants) != 1 {
		t.Fatalf("published grants = %+v", onDisk.Plugins.Grants)
	}
	// .
	for _, f := range grantFields {
		var g broker.Grant
		if !setGrantField(&g, f, true) || grantEmpty(g) {
			t.Fatalf("field %q is offered but not settable", f)
		}
	}
}

// .
// .
// .
// .
func TestPluginGrantAppliesToTheNextCall(t *testing.T) {
	dir := t.TempDir()
	result := genesistest.NewRoot(t).Birth(t, genesis.BirthConfig{
		Name:       "GrantsTest",
		KeyPath:    filepath.Join(dir, "identity.sec"),
		LedgerPath: filepath.Join(dir, "ledger.jsonl"),
		DBPath:     filepath.Join(dir, "aii.db"),
	})
	result.Ledger.Close()

	const id = "org.example.kvpair"
	caps := []string{"ring4.kv"}
	files := map[string][]byte{
		"interfaces/broker.probe.v1.schema.json": []byte(`{"interface":"broker.probe","v":1}`),
		"variants/linux-x86_64-wasm/plugin.wasm": wasmgen.CannedCaller(
			[]byte(`{"operation":"kv.put","target":{"key":"greeting"},"arguments":{"value":"hello-ring4"}}`),
			[]byte(`{"operation":"kv.get","target":{"key":"greeting"},"arguments":{}}`),
		),
	}
	manifest := packagetest.BuildManifestJSON(id, "0.1.0",
		[]packagetest.InterfaceSpec{{ID: "broker.probe", Version: 1, SchemaFile: "interfaces/broker.probe.v1.schema.json", Methods: []string{"roundtrip"}}},
		[]packagetest.VariantSpec{{
			ID: "linux-x86_64-wasm", Platform: packagefmt.HostPlatform(), Arch: packagefmt.HostArch(),
			Topology: packagefmt.HostTopology(), Runtime: "wasm_component", Profile: "wasm_sandbox",
			Entrypoint: "variants/linux-x86_64-wasm/plugin.wasm", Capabilities: caps,
		}},
		files, map[string]interface{}{"capability_envelope": caps})
	pkg := filepath.Join(dir, id+"-0.1.0.aiiospkg")
	if err := os.WriteFile(pkg, packagetest.Build(packagetest.PackageSpec{Root: id + "-0.1.0", Manifest: manifest, InstallFiles: files}), 0o644); err != nil {
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
		Plugins:    PluginsConfig{Autoload: "T0"},
		Agency:     defaultConfig().Agency,
	}
	app := New(cfg)
	if err := startLiveForTest(app); err != nil {
		t.Fatal(err)
	}
	defer app.Stop()
	if len(app.plugins) != 1 {
		t.Fatalf("the plugin must be active: %+v", app.plugins)
	}
	tool := app.plugins[0].ToolNames[0]
	call := func() string {
		res, err := app.toolReg.Execute(context.Background(), tool, nil)
		text := res.Output + " " + res.Error
		if err != nil {
			text += " " + err.Error()
		}
		return text
	}
	hasCap := func(v []string) bool {
		for _, c := range v {
			if c == "ring4.kv" {
				return true
			}
		}
		return false
	}

	// .
	if out := call(); !strings.Contains(out, "POLICY_DENY") {
		t.Fatalf("an ungranted plugin must be refused, got %s", out)
	}
	state, err := app.applyConfigChange(map[string]interface{}{})
	if err != nil {
		t.Fatal(err)
	}
	if v := state.Plugins.Installed[0]; v.Grants.Listed || v.Grants.KV || !hasCap(v.Capabilities) {
		t.Fatalf("the view before any grant: %+v signed for %v", v.Grants, v.Capabilities)
	}

	// .
	state, err = app.applyConfigChange(map[string]interface{}{"plugins.grants." + id + ".kv": true})
	if err != nil {
		t.Fatal(err)
	}
	if v := state.Plugins.Installed[0].Grants; !v.Listed || !v.KV || v.Memory {
		t.Fatalf("the view after the grant: %+v", v)
	}
	if out := call(); !strings.Contains(out, `"stored":true`) || !strings.Contains(out, "hello-ring4") {
		t.Fatalf("the granted plugin's next call must go through: %s", out)
	}

	// .
	state, err = app.applyConfigChange(map[string]interface{}{"plugins.grants." + id + ".kv": false})
	if err != nil {
		t.Fatal(err)
	}
	if v := state.Plugins.Installed[0].Grants; v.Listed || v.KV {
		t.Fatalf("the view after withdrawal: %+v", v)
	}
	if out := call(); !strings.Contains(out, "POLICY_DENY") {
		t.Fatalf("a withdrawn grant must refuse the next call, got %s", out)
	}
}
