package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/broker"
	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/genesis"
	"github.com/aiii-dot-id/aii-os/internal/genesis/genesistest"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt/packagetest"
	"github.com/aiii-dot-id/aii-os/internal/pluginworker/wasmgen"
)

const (
	mailPlugin    = "org.example.mail"
	twoHostPlugin = "org.example.twohosts"
	noHostPlugin  = "org.example.nohost"
	// foreignHostPlugin signs for a host no Google account rides to.
	foreignHostPlugin = "org.example.foreignhost"
	mailHost          = "gmail.googleapis.com:443"
	mailSettings      = `[{"key":"google","type":"secret","title":"Google account","oauth":{"provider":"google","services":["gmail"]}},` +
		`{"key":"api_key","type":"secret","title":"API key"},{"key":"label","type":"string","title":"Label"}]`
)

// settingsPluginSpec is one plugin package: the given guest, the signed
// envelope's outbound hosts, and its settings declaration.
func settingsPluginSpec(id string, outbound []string, settings string, wasm []byte) packagetest.PackageSpec {
	var envelope []string
	for _, h := range outbound {
		envelope = append(envelope, "net.outbound:"+h)
	}
	files := map[string][]byte{
		"interfaces/broker.probe.v1.schema.json": []byte(`{"interface":"broker.probe","v":1}`),
		"variants/linux-x86_64-wasm/plugin.wasm": wasm,
		"settings.json":                          []byte(settings),
	}
	manifest := packagetest.BuildManifestJSON(id, "0.1.0",
		[]packagetest.InterfaceSpec{{ID: "broker.probe", Version: 1, SchemaFile: "interfaces/broker.probe.v1.schema.json", Methods: []string{"probe"}}},
		[]packagetest.VariantSpec{{
			ID: "linux-x86_64-wasm", Platform: packagefmt.HostPlatform(), Arch: packagefmt.HostArch(),
			Topology: packagefmt.HostTopology(), Runtime: "wasm_component", Profile: "wasm_sandbox",
			Entrypoint: "variants/linux-x86_64-wasm/plugin.wasm", Capabilities: envelope,
		}},
		files, map[string]any{"capability_envelope": envelope})
	return packagetest.PackageSpec{Root: id + "-0.1.0", Manifest: manifest, InstallFiles: files}
}

// installSpec writes a package into the identity's plugins directory.
func installSpec(t *testing.T, dir, id string, spec packagetest.PackageSpec) {
	t.Helper()
	pkg := filepath.Join(dir, id+"-0.1.0.aiiospkg")
	if err := os.WriteFile(pkg, packagetest.Build(spec), 0o644); err != nil {
		t.Fatal(err)
	}
	installPluginDir(t, dir, id, pkg)
}

// accountApp boots an identity with the four mail plugins installed, the
// given configuration applied first, and the page's own accounts made
// through the page's own door: google-mail and google-mail2 are Gmail
// accounts, google-cal is set up for the calendar alone, google-narrow is
// a Gmail account whose token the operator narrowed to another host, and
// github-x belongs to another authority.
func accountApp(t *testing.T, configure func(*Config)) (*App, *Config) {
	t.Helper()
	dir := t.TempDir()
	genesistest.NewRoot(t).Birth(t, genesis.BirthConfig{
		Name: "AccountTest", KeyPath: filepath.Join(dir, "identity.sec"),
		LedgerPath: filepath.Join(dir, "ledger.jsonl"), DBPath: filepath.Join(dir, "aii.db"),
	}).Ledger.Close()
	probe := wasmgen.CannedCaller([]byte(`{"operation":"settings.get"}`))
	installSpec(t, dir, mailPlugin, settingsPluginSpec(mailPlugin, []string{mailHost}, mailSettings, probe))
	installSpec(t, dir, twoHostPlugin, settingsPluginSpec(twoHostPlugin, []string{mailHost, "www.googleapis.com:443"}, mailSettings, probe))
	installSpec(t, dir, noHostPlugin, settingsPluginSpec(noHostPlugin, nil, mailSettings, probe))
	installSpec(t, dir, foreignHostPlugin, settingsPluginSpec(foreignHostPlugin, []string{mailHost, "api.example.test:443"}, mailSettings, probe))
	t.Chdir(dir)
	cfg := defaultConfig()
	cfg.Identity = IdentityConfig{KeyPath: filepath.Join(dir, "identity.sec"), LedgerPath: filepath.Join(dir, "ledger.jsonl"), DBPath: filepath.Join(dir, "aii.db")}
	cfg.LLM, cfg.SourcePath, cfg.Dashboard.Port, cfg.Tools.CWD = withTestProvider(t, dir, "test", "https://127.0.0.1:1", "m", "sk-x"), filepath.Join(dir, "config.json"), 0, dir
	cfg.Plugins.Autoload = "T0"
	cfg.Plugins.AuthProfiles = map[string]broker.AuthProfile{"static-key": {SecretEnv: "ACCOUNT_TEST_KEY", Host: "gmail.googleapis.com", Port: 443}}
	if configure != nil {
		configure(cfg)
	}
	app := New(cfg)
	if err := startLiveForTest(app); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stopApp(t, app) })
	if len(app.plugins) != 4 {
		t.Fatalf("the four plugins must be active: %d", len(app.plugins))
	}
	for _, edit := range []dashboard.AuthProfileEdit{
		{Name: "google-mail", Provider: "google", ClientID: "cid", Services: map[string]string{"gmail": "read"}},
		{Name: "google-mail2", Provider: "google", ClientID: "cid", Services: map[string]string{"gmail": "modify"}},
		{Name: "google-cal", Provider: "google", ClientID: "cid", Services: map[string]string{"calendar": "read"}},
		{Name: "google-narrow", Provider: "google", ClientID: "cid", Services: map[string]string{"gmail": "read"}, Hosts: []string{"www.googleapis.com:443"}},
		{Name: "github-x", Provider: "github", ClientID: "cid", Services: map[string]string{"issues": "read"}},
	} {
		if err := app.SetAuthProfile(edit); err != nil {
			t.Fatalf("%s: %v", edit.Name, err)
		}
	}
	// The caller gets a copy: the config watcher rewrites the live one as the
	// file changes, and a test reading it through this pointer races it.
	live := app.configSnapshot()
	return app, &live
}

// cardSetting is one setting as the plugins page receives it.
func cardSetting(t *testing.T, a *App, id, key string) dashboard.PluginSettingView {
	t.Helper()
	for _, p := range a.configState().Plugins.Installed {
		if p.ID != id {
			continue
		}
		for _, s := range p.Settings {
			if s.Key == key {
				return s
			}
		}
	}
	t.Fatalf("no setting %s on %s's card", key, id)
	return dashboard.PluginSettingView{}
}

// wholeConfig is the live configuration, deep, for comparing before and
// after: a snapshot shares its maps with the live one.
func wholeConfig(t *testing.T, a *App) []byte {
	t.Helper()
	raw, err := json.Marshal(a.configSnapshot())
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func readFileT(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestAPluginIsConnectedToAnAccountFromItsCard(t *testing.T) {
	logs := logsink.CaptureForTest(t)
	app, cfg := accountApp(t, func(c *Config) {
		// A grant the plugin already holds, so the map a candidate must
		// copy exists; and the no-host plugin configured by hand before
		// the card could do it, its grant holding the handle alone.
		c.Plugins.Grants = map[string]broker.Grant{mailPlugin: {ReadOnly: true}, noHostPlugin: {CredentialHandles: []string{"static-key"}}}
		c.Plugins.Settings = map[string]map[string]any{noHostPlugin: {"google": "static-key"}}
	})
	key := "plugins.settings." + mailPlugin + ".google"

	// The card before: the package's one host, the Gmail accounts — those
	// that cannot be chosen saying why — and nothing granted.
	v := cardSetting(t, app, mailPlugin, "google")
	if v.Account == nil || len(v.Account.Hosts) != 1 || v.Account.Hosts[0] != mailHost || v.Account.Not != "" || v.Account.Granted {
		t.Fatalf("the card offers the package's one host: %+v", v.Account)
	}
	var names []string
	for _, o := range v.Account.Offers {
		names = append(names, o.Name)
		switch o.Name {
		case "google-mail", "google-mail2":
			if o.Not != "" || o.State != "disconnected" {
				t.Errorf("%s can be chosen, and is not connected yet: %+v", o.Name, o)
			}
		case "google-cal":
			if !strings.Contains(o.Not, "was not set up for gmail") {
				t.Errorf("google-cal says why it cannot be chosen: %+v", o)
			}
		case "google-narrow":
			if !strings.Contains(o.Not, "rides only to www.googleapis.com:443, not to "+mailHost) {
				t.Errorf("google-narrow says why it cannot be chosen: %+v", o)
			}
		}
	}
	if strings.Join(names, ",") != "google-cal,google-mail,google-mail2,google-narrow" {
		t.Fatalf("the card lists its authority's accounts only: %v", names)
	}

	if v := cardSetting(t, app, twoHostPlugin, "google"); v.Account == nil || v.Account.Not != "" || strings.Join(v.Account.Hosts, ",") != mailHost+",www.googleapis.com:443" {
		t.Fatalf("a package with two hosts the account rides to is offered it: %+v", v.Account)
	} else {
		for _, o := range v.Account.Offers {
			switch o.Name {
			case "google-mail":
				if o.Not != "" {
					t.Errorf("google-mail rides to both hosts: %+v", o)
				}
			case "google-narrow":
				if !strings.Contains(o.Not, "rides only to www.googleapis.com:443, not to "+mailHost+", which this plugin's signed package also sends its requests to") {
					t.Errorf("google-narrow rides to one of the two: %+v", o)
				}
			}
		}
	}
	if v := cardSetting(t, app, noHostPlugin, "google"); v.Account == nil || len(v.Account.Hosts) != 0 || !strings.Contains(v.Account.Not, "declares no outbound host") {
		t.Fatalf("a package with no host says why it cannot connect an account: %+v", v.Account)
	}
	if v := cardSetting(t, app, mailPlugin, "api_key"); v.Account != nil {
		t.Fatalf("a secret with no hint has no connect step: %+v", v.Account)
	}

	// Save: one persist, carrying the setting, the handle and the host.
	var commits []Config
	persist := func(c *Config) (bool, error) {
		commits = append(commits, *c)
		return saveConfig(c)
	}
	if _, err := app.applyConfigChangeWith(map[string]any{key: "google-mail"}, persist); err != nil {
		t.Fatal(err)
	}
	if len(commits) != 1 {
		t.Fatalf("one save is one commit: %d", len(commits))
	}
	for _, c := range []Config{commits[0], app.configSnapshot(), loadT(t, cfg.SourcePath)} {
		g := c.Plugins.Grants[mailPlugin]
		if c.Plugins.Settings[mailPlugin]["google"] != "google-mail" || !slices.Equal(g.CredentialHandles, []string{"google-mail"}) || !slices.Equal(g.Hosts, []string{mailHost}) || !g.ReadOnly {
			t.Fatalf("the setting, the handle and the package's host land together, and nothing else changes: %v %+v", c.Plugins.Settings[mailPlugin], g)
		}
	}
	if v := cardSetting(t, app, mailPlugin, "google"); v.Value != "google-mail" || !v.Account.Granted || !slices.Contains(v.Handles, "google-mail") {
		t.Fatalf("the card says what is granted: %+v", v)
	}

	// The same choice again changes nothing, and says so.
	saved := readFileT(t, cfg.SourcePath)
	if _, err := app.applyConfigChange(map[string]any{key: "google-mail"}); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(readFileT(t, cfg.SourcePath), saved) {
		t.Fatal("the same choice again rewrote the configuration")
	}
	if !logs.Contains("google-mail is already its account for google, granted with " + mailHost + " — nothing changed") {
		t.Fatalf("the same choice again says it changed nothing: %s", logs.String())
	}

	// Another choice moves the handle with it; the host is granted once.
	if _, err := app.applyConfigChange(map[string]any{key: "google-mail2"}); err != nil {
		t.Fatal(err)
	}
	if g := app.configSnapshot().Plugins.Grants[mailPlugin]; !slices.Equal(g.CredentialHandles, []string{"google-mail2"}) || !slices.Equal(g.Hosts, []string{mailHost}) {
		t.Fatalf("the grant follows the choice: %+v", g)
	}

	// A save that cannot persist changes nothing live, and nothing on disk.
	live, disk := wholeConfig(t, app), readFileT(t, cfg.SourcePath)
	if _, err := app.applyConfigChangeWith(map[string]any{key: "google-mail"}, func(*Config) (bool, error) {
		return false, errors.New("the disk is full")
	}); err == nil {
		t.Fatal("a save that did not persist must fail")
	}
	if !bytes.Equal(wholeConfig(t, app), live) || !bytes.Equal(readFileT(t, cfg.SourcePath), disk) {
		t.Fatal("a save that did not persist changed the live configuration or the file")
	}

	// Remove through the config door: the handle goes, the host stays.
	if _, err := app.applyConfigChange(map[string]any{key: nil}); err != nil {
		t.Fatal(err)
	}
	c := app.configSnapshot()
	if g := c.Plugins.Grants[mailPlugin]; len(g.CredentialHandles) != 0 || !slices.Equal(g.Hosts, []string{mailHost}) || !g.ReadOnly {
		t.Fatalf("remove takes the handle back and keeps the host: %+v", g)
	}
	if _, set := c.Plugins.Settings[mailPlugin]["google"]; set {
		t.Fatal("remove forgets the choice")
	}
	// … and through the card's Remove, the key door, the same way.
	if _, err := app.applyConfigChange(map[string]any{key: "google-mail"}); err != nil {
		t.Fatal(err)
	}
	if err := app.ClearPluginSecret(mailPlugin, "google"); err != nil {
		t.Fatal(err)
	}
	c = app.configSnapshot()
	if g := c.Plugins.Grants[mailPlugin]; len(g.CredentialHandles) != 0 || !slices.Equal(g.Hosts, []string{mailHost}) {
		t.Fatalf("the card's Remove takes the handle back and keeps the host: %+v", g)
	}
	if _, set := c.Plugins.Settings[mailPlugin]["google"]; set {
		t.Fatal("the card's Remove forgets the choice")
	}

	// A grant left granting nothing is removed, as the grant door removes one.
	if _, err := app.applyConfigChange(map[string]any{"plugins.settings." + noHostPlugin + ".google": nil}); err != nil {
		t.Fatal(err)
	}
	if g, listed := app.configSnapshot().Plugins.Grants[noHostPlugin]; listed {
		t.Fatalf("a grant that grants nothing is not left listed: %+v", g)
	}

	// Concurrent saves serialize: each lands whole or is refused as stale,
	// and what is live is what is on disk — one choice, its handle alone.
	var wg sync.WaitGroup
	errs := make([]error, 8)
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = app.applyConfigChange(map[string]any{key: []string{"google-mail", "google-mail2"}[i%2]})
		}(i)
	}
	wg.Wait()
	landed := 0
	for _, err := range errs {
		var refusal *AccountRefusal
		switch {
		case err == nil:
			landed++
		case errors.As(err, &refusal):
			t.Errorf("a good choice was refused on its merits: %v", err)
		}
	}
	if landed == 0 {
		t.Fatal("no concurrent save landed")
	}
	c = app.configSnapshot()
	chosen, _ := c.Plugins.Settings[mailPlugin]["google"].(string)
	if g := c.Plugins.Grants[mailPlugin]; !slices.Equal(g.CredentialHandles, []string{chosen}) || !slices.Equal(g.Hosts, []string{mailHost}) {
		t.Fatalf("after concurrent saves the grant is the one choice's: %q %+v", chosen, g)
	}
	onDisk := loadT(t, cfg.SourcePath)
	if !jsonEqual(t, onDisk.Plugins.Grants, c.Plugins.Grants) || !jsonEqual(t, onDisk.Plugins.Settings, c.Plugins.Settings) {
		t.Fatal("after concurrent saves the live configuration and the file disagree")
	}

	// The card says when the chosen account no longer exists.
	if err := app.DeleteAuthProfile(chosen); err != nil {
		t.Fatal(err)
	}
	v = cardSetting(t, app, mailPlugin, "google")
	for _, o := range v.Account.Offers {
		if o.Name == chosen {
			t.Fatalf("a deleted account is not offered: %+v", v.Account.Offers)
		}
	}
	if v.Value != chosen {
		t.Fatalf("the choice stays visible as stored, for the card to say it is gone: %+v", v)
	}
}

func TestAnAccountTheCardCannotConnectWritesNothing(t *testing.T) {
	app, cfg := accountApp(t, nil)
	disk, live := readFileT(t, cfg.SourcePath), wholeConfig(t, app)
	for _, c := range []struct{ plugin, handle, requirement, says string }{
		{mailPlugin, "nobody", accountNeedsProfile, "nobody is not an account on this identity"},
		{mailPlugin, "static-key", accountNeedsProfile, "static-key is a bearer key, not an account"},
		{mailPlugin, "github-x", accountNeedsProvider, "github-x is a github account, and this setting asks for a google one"},
		{mailPlugin, "google-cal", accountNeedsServices, "google-cal was not set up for gmail"},
		{mailPlugin, "google-narrow", accountNeedsRide, "google-narrow's token rides only to www.googleapis.com:443, not to " + mailHost},
		{twoHostPlugin, "google-narrow", accountNeedsRide, "not to " + mailHost + ", which this plugin's signed package also sends its requests to — every call to " + mailHost + " would be refused"},
		{foreignHostPlugin, "google-mail", accountNeedsRide, "not to api.example.test:443, which this plugin's signed package also sends its requests to"},
		{noHostPlugin, "google-mail", accountNeedsHosts, "declares no outbound host in its signed package"},
	} {
		_, err := app.applyConfigChange(map[string]any{"plugins.settings." + c.plugin + ".google": c.handle})
		var refusal *AccountRefusal
		if !errors.As(err, &refusal) || refusal.Requirement != c.requirement || !strings.Contains(err.Error(), c.says) {
			t.Errorf("%s for %s: want the %s refusal saying %q, got %v", c.handle, c.plugin, c.requirement, c.says, err)
		}
		if !bytes.Equal(readFileT(t, cfg.SourcePath), disk) || !bytes.Equal(wholeConfig(t, app), live) {
			t.Fatalf("the refusal of %s for %s wrote something", c.handle, c.plugin)
		}
	}

	// A secret with no hint is what it was (S6): a handle a profile has is
	// stored, and nothing is granted with it.
	if _, err := app.applyConfigChange(map[string]any{"plugins.settings." + mailPlugin + ".api_key": "google-mail"}); err != nil {
		t.Fatal(err)
	}
	c := app.configSnapshot()
	if c.Plugins.Settings[mailPlugin]["api_key"] != "google-mail" {
		t.Fatalf("the S6 door stores the handle: %v", c.Plugins.Settings[mailPlugin])
	}
	if g, listed := c.Plugins.Grants[mailPlugin]; listed {
		t.Fatalf("a setting with no hint grants nothing: %+v", g)
	}

	// The grant door refuses the lists and names the routes that grant them.
	for _, field := range []string{"hosts", "credential_handles", "local"} {
		_, err := app.applyConfigChange(map[string]any{"plugins.grants." + mailPlugin + "." + field: true})
		if err == nil || !strings.Contains(err.Error(), "an account chosen on it") || !strings.Contains(err.Error(), "a key pasted on the plugin's card") {
			t.Errorf("the grant door refuses %s and names the card's routes: %v", field, err)
		}
	}
}

func loadT(t *testing.T, path string) Config {
	t.Helper()
	c, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	return *c
}

func jsonEqual(t *testing.T, a, b any) bool {
	t.Helper()
	x, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	y, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	return bytes.Equal(x, y)
}

func TestClearingTheLastSettingLeavesTheConfigurationAReloadReads(t *testing.T) {
	app, _ := accountApp(t, nil)
	reread := func(c Config) Config {
		t.Helper()
		raw, err := json.Marshal(c)
		if err != nil {
			t.Fatal(err)
		}
		var back Config
		if err := json.Unmarshal(raw, &back); err != nil {
			t.Fatal(err)
		}
		return back
	}
	stored := func(settings map[string]map[string]any) Config {
		c := app.configSnapshot()
		c.Plugins.Settings = settings
		return c
	}

	last := stored(map[string]map[string]any{twoHostPlugin: {"google": "google-mail"}})
	if err := app.applyPluginSetting(&last, "plugins.settings."+twoHostPlugin+".google", nil); err != nil {
		t.Fatal(err)
	}
	if back := reread(last); !reflect.DeepEqual(last.Plugins.Settings, back.Plugins.Settings) {
		t.Fatalf("the last setting cleared leaves %#v, and a reload of that save reads %#v", last.Plugins.Settings, back.Plugins.Settings)
	}

	// Another plugin's settings stay, and are what a reload reads.
	one := stored(map[string]map[string]any{twoHostPlugin: {"google": "google-mail"}, mailPlugin: {"google": "google-mail"}})
	if err := app.applyPluginSetting(&one, "plugins.settings."+twoHostPlugin+".google", nil); err != nil {
		t.Fatal(err)
	}
	if want := map[string]map[string]any{mailPlugin: {"google": "google-mail"}}; !reflect.DeepEqual(one.Plugins.Settings, want) {
		t.Fatalf("the other plugin's setting stays: %#v", one.Plugins.Settings)
	}
	if back := reread(one); !reflect.DeepEqual(one.Plugins.Settings, back.Plugins.Settings) {
		t.Fatalf("what is left is %#v, and a reload reads %#v", one.Plugins.Settings, back.Plugins.Settings)
	}
}

func TestAnAccountIsGrantedEveryHostThePackageSigns(t *testing.T) {
	app, cfg := accountApp(t, nil)
	key := "plugins.settings." + twoHostPlugin + ".google"
	both := []string{mailHost, "www.googleapis.com:443"}

	if _, err := app.applyConfigChange(map[string]any{key: "google-narrow"}); err == nil || !strings.Contains(err.Error(), "which this plugin's signed package also sends its requests to") {
		t.Fatalf("an account that rides to one of the two is refused, naming the other: %v", err)
	}
	if g, listed := app.configSnapshot().Plugins.Grants[twoHostPlugin]; listed {
		t.Fatalf("the refusal wrote a grant: %+v", g)
	}

	if _, err := app.applyConfigChange(map[string]any{key: "google-mail"}); err != nil {
		t.Fatal(err)
	}
	for _, c := range []Config{app.configSnapshot(), loadT(t, cfg.SourcePath)} {
		g := c.Plugins.Grants[twoHostPlugin]
		if !slices.Equal(g.CredentialHandles, []string{"google-mail"}) || !slices.Equal(g.Hosts, both) {
			t.Fatalf("the handle and both hosts land together: %+v", g)
		}
	}
	if v := cardSetting(t, app, twoHostPlugin, "google"); !v.Account.Granted || strings.Join(v.Account.Hosts, ",") != strings.Join(both, ",") {
		t.Fatalf("the card says both hosts are granted: %+v", v.Account)
	}

	// A grant that holds only one of the two is not "granted": the card offers to complete it.
	if _, err := app.applyConfigChange(map[string]any{"plugins.settings." + twoHostPlugin + ".google": nil}); err != nil {
		t.Fatal(err)
	}
	if g := app.configSnapshot().Plugins.Grants[twoHostPlugin]; !slices.Equal(g.Hosts, both) || len(g.CredentialHandles) != 0 {
		t.Fatalf("Remove takes the handle back and keeps both hosts: %+v", g)
	}

	// The same choice again changes nothing.
	before := wholeConfig(t, app)
	if _, err := app.applyConfigChange(map[string]any{key: "google-mail"}); err != nil {
		t.Fatal(err)
	}
	if _, err := app.applyConfigChange(map[string]any{key: "google-mail"}); err != nil {
		t.Fatal(err)
	}
	if after := wholeConfig(t, app); !bytes.Equal(before, after) && !slices.Equal(app.configSnapshot().Plugins.Grants[twoHostPlugin].Hosts, both) {
		t.Fatalf("the same choice again changed the grant")
	}
}

// A grant that holds the handle and only some of the signed hosts is not
// "granted": the card says so, and Save completes it.
func TestAGrantOfSomeOfTheHostsIsNotComplete(t *testing.T) {
	app, _ := accountApp(t, func(c *Config) {
		c.Plugins.Grants = map[string]broker.Grant{twoHostPlugin: {CredentialHandles: []string{"google-mail"}, Hosts: []string{mailHost}}}
		c.Plugins.Settings = map[string]map[string]any{twoHostPlugin: {"google": "google-mail"}}
	})
	if v := cardSetting(t, app, twoHostPlugin, "google"); v.Account == nil || v.Account.Granted {
		t.Fatalf("one of two hosts is not granted: %+v", v.Account)
	}
	if _, err := app.applyConfigChange(map[string]any{"plugins.settings." + twoHostPlugin + ".google": "google-mail"}); err != nil {
		t.Fatal(err)
	}
	g := app.configSnapshot().Plugins.Grants[twoHostPlugin]
	if !slices.Equal(g.Hosts, []string{mailHost, "www.googleapis.com:443"}) {
		t.Fatalf("Save completes the grant: %+v", g)
	}
	if v := cardSetting(t, app, twoHostPlugin, "google"); !v.Account.Granted {
		t.Fatalf("and the card says so: %+v", v.Account)
	}
}
