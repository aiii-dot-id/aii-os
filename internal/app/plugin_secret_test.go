package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/broker"
	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/fileperm"
	"github.com/aiii-dot-id/aii-os/internal/genesis"
	"github.com/aiii-dot-id/aii-os/internal/genesis/genesistest"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt/packagetest"
	"github.com/aiii-dot-id/aii-os/internal/pluginworker/wasmgen"
)

// keyedPluginConfig births an identity and installs one plugin whose signed
// envelope declares the given outbound hosts and whose settings declare a
// secret api_key and a plain model.
func keyedPluginConfig(t *testing.T, id string, outbound ...string) (*Config, string) {
	t.Helper()
	dir := t.TempDir()
	result := genesistest.NewRoot(t).Birth(t, genesis.BirthConfig{
		Name: "KeyTest", KeyPath: filepath.Join(dir, "identity.sec"),
		LedgerPath: filepath.Join(dir, "ledger.jsonl"), DBPath: filepath.Join(dir, "aii.db"),
	})
	result.Ledger.Close()
	var envelope []string
	for _, h := range outbound {
		envelope = append(envelope, "net.outbound:"+h)
	}
	files := map[string][]byte{
		"interfaces/key.probe.v1.schema.json":    []byte(`[{"id":"probe.settings","summary":"Read my settings","effects":"read.internal","capabilities":[]}]`),
		"variants/linux-x86_64-wasm/plugin.wasm": wasmgen.CannedCaller([]byte(`{"operation":"settings.get"}`)),
		"settings.json":                          []byte(`[{"key":"api_key","type":"secret","title":"Jev API key"},{"key":"model","type":"string","title":"Model"}]`),
	}
	manifest := packagetest.BuildManifestJSON(id, "0.1.0",
		[]packagetest.InterfaceSpec{{ID: "key.probe", Version: 1, SchemaFile: "interfaces/key.probe.v1.schema.json", Methods: []string{"probe.settings"}}},
		[]packagetest.VariantSpec{{
			ID: "linux-x86_64-wasm", Platform: packagefmt.HostPlatform(), Arch: packagefmt.HostArch(),
			Topology: packagefmt.HostTopology(), Runtime: "wasm_component", Profile: "wasm_sandbox",
			Entrypoint: "variants/linux-x86_64-wasm/plugin.wasm", Capabilities: envelope,
		}},
		files, map[string]any{"capability_envelope": envelope})
	pkg := filepath.Join(dir, id+"-0.1.0.aiiospkg")
	if err := os.WriteFile(pkg, packagetest.Build(packagetest.PackageSpec{Root: id + "-0.1.0", Manifest: manifest, InstallFiles: files}), 0o644); err != nil {
		t.Fatal(err)
	}
	installPluginDir(t, dir, id, pkg)
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
	return cfg, dir
}

func secretView(t *testing.T, a *App, id string) dashboard.PluginSettingView {
	t.Helper()
	state, err := a.applyConfigChange(map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range state.Plugins.Installed {
		if p.ID != id {
			continue
		}
		for _, s := range p.Settings {
			if s.Key == "api_key" {
				return s
			}
		}
	}
	t.Fatalf("no api_key setting on %s's card", id)
	return dashboard.PluginSettingView{}
}

func TestAPluginKeyPastedOnItsCardIsKeptAndWired(t *testing.T) {
	const id = "org.example.keyed"
	const pasted = "sk-jev-PASTED-0123456789"
	cfg, dir := keyedPluginConfig(t, id, "api.jev.test:443")
	logs := logsink.CaptureForTest(t)
	app := New(cfg)
	if err := startLiveForTest(app); err != nil {
		t.Fatal(err)
	}
	stopped := false
	defer func() {
		if !stopped {
			stopApp(t, app)
		}
	}()

	if v := secretView(t, app, id); v.KeyHost != "api.jev.test:443" || v.KeyKept || v.KeyNot != "" {
		t.Fatalf("before a paste the card offers the key for the package's one host: %+v", v)
	}

	for _, bad := range []struct{ plugin, key, secret string }{
		{id, "api_key", "   "},
		{id, "api_key", "sk-one\nsk-two"},
		{id, "api_key", strings.Repeat("k", broker.MaxCredentialBytes+1)},
		{id, "model", "sk-x"},
		{id, "colour", "sk-x"},
		{"org.example.absent", "api_key", "sk-x"},
	} {
		if err := app.SetPluginSecret(bad.plugin, bad.key, bad.secret); err == nil {
			t.Fatalf("%s.%s=%q must be refused", bad.plugin, bad.key, bad.secret)
		}
	}
	if len(app.configSnapshot().Plugins.AuthProfiles) != 0 {
		t.Fatal("a refused paste stored a profile")
	}

	if err := app.SetPluginSecret(id, "api_key", "  "+pasted+"  "); err != nil {
		t.Fatal(err)
	}
	name := keyProfileName(id, "api_key")
	path := filepath.Join(dir, "credentials", "profiles", name+".key")
	c := app.configSnapshot()
	prof, ok := c.Plugins.AuthProfiles[name]
	if !ok || prof.Scheme != "bearer" || prof.Host != "api.jev.test" || prof.Port != 443 || !samePath(prof.SecretFile, path) {
		t.Fatalf("the profile rides the kept file to the declared host only: %+v", prof)
	}
	g := c.Plugins.Grants[id]
	if len(g.CredentialHandles) != 1 || g.CredentialHandles[0] != name || len(g.Hosts) != 1 || g.Hosts[0] != "api.jev.test:443" {
		t.Fatalf("the plugin is granted the handle and the host: %+v", g)
	}
	if c.Plugins.Settings[id]["api_key"] != name {
		t.Fatalf("the setting names the handle: %v", c.Plugins.Settings[id])
	}
	raw, err := os.ReadFile(path)
	if err != nil || string(raw) != pasted+"\n" {
		t.Fatalf("the key is kept, trimmed: %q %v", raw, err)
	}
	if runtime.GOOS != "windows" {
		if st, _ := os.Stat(path); st.Mode().Perm() != 0o600 {
			t.Fatalf("the key file is owner-only: %v", st.Mode())
		}
	}
	if ok, err := fileperm.IsRestrictedToOwner(path); err != nil || !ok {
		t.Fatalf("the key file is readable beyond its owner: %v", err)
	}
	saved, _ := os.ReadFile(cfg.SourcePath)
	view := secretView(t, app, id)
	viewJSON, _ := json.Marshal(view)
	if strings.Contains(string(saved), pasted) || strings.Contains(string(viewJSON), pasted) {
		t.Fatal("the key reached the config file or the page")
	}
	if !view.KeyKept || view.Value != name {
		t.Fatalf("the card says a key is kept: %+v", view)
	}
	if logs.Contains(pasted) || !logs.Contains("kept as profile "+name) {
		t.Fatal("the paste is logged by name, never by value")
	}

	// Replace: the file changes, the wiring does not.
	if err := app.SetPluginSecret(id, "api_key", "sk-jev-REPLACED"); err != nil {
		t.Fatal(err)
	}
	if raw, _ := os.ReadFile(path); string(raw) != "sk-jev-REPLACED\n" {
		t.Fatalf("replace rewrites the kept key: %q", raw)
	}
	if g := app.configSnapshot().Plugins.Grants[id]; len(g.CredentialHandles) != 1 || len(g.Hosts) != 1 {
		t.Fatalf("replace adds nothing twice: %+v", g)
	}

	// Restart: what config.json persisted names the file; the next boot
	// reads it.
	onDisk, err := os.ReadFile(cfg.SourcePath)
	if err != nil {
		t.Fatal(err)
	}
	var persisted Config
	if err := json.Unmarshal(onDisk, &persisted); err != nil {
		t.Fatal(err)
	}
	stopApp(t, app)
	stopped = true
	cfg2 := *cfg
	cfg2.Plugins = persisted.Plugins
	app2 := New(&cfg2)
	if err := startLiveForTest(app2); err != nil {
		t.Fatal(err)
	}
	defer stopApp(t, app2)
	if v := secretView(t, app2, id); !v.KeyKept || v.Value != name {
		t.Fatalf("after a restart the card still has its key: %+v", v)
	}

	second := filepath.Join(filepath.Dir(path), "second-name")
	linked := os.Link(path, second) == nil
	if err := app2.ClearPluginSecret(id, "api_key"); err != nil {
		t.Fatal(err)
	}
	if linked {
		if got, err := os.ReadFile(second); err != nil || len(got) != 0 {
			t.Errorf("a name made for the key file holds nothing of it after the key is forgotten: %q %v", got, err)
		}
	} else {
		t.Log("hard links unavailable here: the second name is not asked")
	}
	c = app2.configSnapshot()
	if _, ok := c.Plugins.AuthProfiles[name]; ok {
		t.Fatal("remove forgets the card's profile")
	}
	if g := c.Plugins.Grants[id]; len(g.CredentialHandles) != 0 || len(g.Hosts) != 1 {
		t.Fatalf("remove takes back the handle and keeps the host grant: %+v", g)
	}
	if _, set := c.Plugins.Settings[id]["api_key"]; set {
		t.Fatal("remove clears the setting")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("remove deletes the key file: %v", err)
	}
	if v := secretView(t, app2, id); v.KeyKept {
		t.Fatalf("the card no longer says a key is kept: %+v", v)
	}
}

// A package that declares no single outbound host gives the card no
// destination for a key: the card says why, and a paste is refused.
func TestAPluginKeyNeedsOneDeclaredHost(t *testing.T) {
	const id = "org.example.twohosts"
	cfg, _ := keyedPluginConfig(t, id, "api.one.test:443", "api.two.test:443")
	app := New(cfg)
	if err := startLiveForTest(app); err != nil {
		t.Fatal(err)
	}
	defer stopApp(t, app)
	if v := secretView(t, app, id); v.KeyHost != "" || !strings.Contains(v.KeyNot, "2 outbound hosts") {
		t.Fatalf("the card says why it cannot take a key: %+v", v)
	}
	if err := app.SetPluginSecret(id, "api_key", "sk-x"); err == nil || !strings.Contains(err.Error(), "2 outbound hosts") {
		t.Fatalf("a paste is refused by name: %v", err)
	}
	if len(app.configSnapshot().Plugins.AuthProfiles) != 0 {
		t.Fatal("a refused paste stored a profile")
	}
}

// A profile the operator wrote under the card's name is theirs: a paste
// does not overwrite it, and a Remove only unselects it.
func TestAPluginKeyNeverTakesOverAnOperatorProfile(t *testing.T) {
	const id = "org.example.owned"
	cfg, _ := keyedPluginConfig(t, id, "api.jev.test:443")
	name := keyProfileName(id, "api_key")
	cfg.Plugins.AuthProfiles = map[string]broker.AuthProfile{name: {SecretEnv: "OPERATOR_KEY", Host: "api.jev.test", Port: 443}}
	cfg.Plugins.Grants = map[string]broker.Grant{id: {CredentialHandles: []string{name}, Hosts: []string{"api.jev.test:443"}}}
	cfg.Plugins.Settings = map[string]map[string]any{id: {"api_key": name}}
	app := New(cfg)
	if err := startLiveForTest(app); err != nil {
		t.Fatal(err)
	}
	defer stopApp(t, app)
	if err := app.SetPluginSecret(id, "api_key", "sk-x"); err == nil {
		t.Fatal("a paste must not overwrite the operator's profile")
	}
	if err := app.ClearPluginSecret(id, "api_key"); err != nil {
		t.Fatal(err)
	}
	c := app.configSnapshot()
	if p, ok := c.Plugins.AuthProfiles[name]; !ok || p.SecretEnv != "OPERATOR_KEY" {
		t.Fatalf("remove leaves the operator's profile: %+v", c.Plugins.AuthProfiles)
	}
	if g := c.Plugins.Grants[id]; len(g.CredentialHandles) != 1 {
		t.Fatalf("remove leaves the operator's grant: %+v", g)
	}
	if _, set := c.Plugins.Settings[id]["api_key"]; set {
		t.Fatal("remove unselects the profile")
	}
}
