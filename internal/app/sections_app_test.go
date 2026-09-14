package app

// .
// .
// .
// .
// .
// .

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/genesis"
	"github.com/aiii-dot-id/aii-os/internal/genesis/genesistest"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt/packagetest"
)

func buildSectionPkg(t *testing.T, dir, id, sectionID string) string {
	t.Helper()
	files := map[string][]byte{
		"section.json": []byte(`{"id":"` + sectionID + `","title":"Hello","slot":"panel",` +
			`"commands":["project.select"],"topics":["status"],"entry":"index.html"}`),
		"index.html": []byte("<!DOCTYPE html><script type=\"module\" src=\"./module.js\"></script>"),
		"module.js":  []byte("import { ready } from '/section-api.js';\nconst api = await ready();\napi.tokens(() => {});\nawait api.data.subscribe('status', s => { api.resize(120); });\napi.act('project.select', { id: 'p1' }).catch(() => {});\n"),
		"styles.css": []byte("body{color:inherit}"),
	}
	return writeAssetPkg(t, dir, id, files)
}

func writeAssetPkg(t *testing.T, dir, id string, files map[string][]byte) string {
	t.Helper()
	manifest, err := json.Marshal(map[string]interface{}{
		"kind": "asset", "id": id, "version": "0.1.0",
		"package_hash": packagetest.ReferencePackageHash(files),
		"asset_type":   "static_template",
	})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, id+"-0.1.0.aiiospkg")
	if err := os.WriteFile(path, packagetest.Build(packagetest.PackageSpec{
		Root: id + "-0.1.0", Manifest: manifest, InstallFiles: files,
	}), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSectionPackagesActivateAtStartup(t *testing.T) {
	dir := t.TempDir()
	result := genesistest.NewRoot(t).Birth(t, genesis.BirthConfig{
		Name:       "SectionTest",
		KeyPath:    filepath.Join(dir, "identity.sec"),
		LedgerPath: filepath.Join(dir, "ledger.jsonl"),
		DBPath:     filepath.Join(dir, "aii.db"),
	})
	result.Ledger.Close()

	sectionPkg := buildSectionPkg(t, dir, "org.example.hello", "hello")
	plainAsset := writeAssetPkg(t, dir, "org.example.prompts", map[string][]byte{"prompts/hi.md": []byte("# hi\n")})
	pluginPkg := buildResponderPkg(t, dir, "org.example.good")
	installPluginDir(t, dir, "org.example.hello", sectionPkg)
	installPluginDir(t, dir, "org.example.prompts", plainAsset)
	installPluginDir(t, dir, "org.example.good", pluginPkg)
	t.Chdir(dir)

	// .
	devDir := filepath.Join(dir, "devsec")
	if err := os.MkdirAll(devDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for rel, body := range map[string]string{
		"section.json": `{"id":"devsec","slot":"rail"}`,
		"index.html":   "<!DOCTYPE html>dev",
	} {
		if err := os.WriteFile(filepath.Join(devDir, rel), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	cfg := &Config{
		Identity: IdentityConfig{KeyPath: filepath.Join(dir, "identity.sec"),
			LedgerPath: filepath.Join(dir, "ledger.jsonl"), DBPath: filepath.Join(dir, "aii.db"),
		},
		LLM:        withTestProvider(t, dir, "test", "https://127.0.0.1:1", "m", "sk-x"),
		SourcePath: filepath.Join(dir, "config.json"),
		Dashboard:  DashboardConfig{Port: 0},
		Tools:      ToolsConfig{CWD: dir},
		Plugins: PluginsConfig{
			Autoload:   "T0",
			DevSection: &DevSectionConfig{ID: "devsec", Path: devDir},
		},
		Agency: defaultConfig().Agency,
	}
	app := New(cfg)
	if err := startLiveForTest(app); err != nil {
		t.Fatalf("mixed packages must never fail boot: %v", err)
	}
	defer app.Stop()

	// .
	sec, ok := app.sections.Get("hello")
	if !ok || sec.Dev || sec.PackageID != "org.example.hello" || sec.Decl.Slot != "panel" {
		t.Fatalf("section not registered from config: %+v (ok=%v)", sec, ok)
	}
	if len(sec.Decl.Commands) != 1 || sec.Decl.Commands[0] != "project.select" {
		t.Fatalf("declaration must ride the registry: %+v", sec.Decl)
	}
	cacheDir := sec.Dir

	// .
	dev, ok := app.sections.Get("devsec")
	if !ok || !dev.Dev || dev.Dir != devDir {
		t.Fatalf("dev section not registered: %+v (ok=%v)", dev, ok)
	}

	// .
	if _, ok := app.sections.Get("org.example.prompts"); ok {
		t.Fatal("an asset without section.json must not register")
	}
	if len(app.sections.List()) != 2 {
		t.Fatalf("exactly hello+devsec must be registered: %v", app.sections.List())
	}

	// .
	if _, ok := app.toolReg.Get("pl_org_example_good_ping"); !ok {
		t.Fatalf("plugin package must still activate through the harness, names: %v", app.toolReg.Names())
	}

	// .
	// .
	app.Stop()
	if _, err := os.Stat(cacheDir); !os.IsNotExist(err) {
		t.Fatal("Stop must remove the section extraction cache")
	}
	if _, err := os.Stat(filepath.Join(devDir, "section.json")); err != nil {
		t.Fatal("Stop must not touch the dev directory")
	}
}

// .
// .
// .
// .
// .
func TestDevSectionNotSettableViaDashboard(t *testing.T) {
	a, _ := newSandboxApp(t)
	for _, key := range []string{"plugins.dev_section", "plugins.dev_section.id", "plugins.dev_section.path"} {
		if _, err := a.applyConfigChange(map[string]interface{}{key: map[string]interface{}{"id": "x", "path": "/tmp/x"}}); err == nil {
			t.Fatalf("%s must be rejected by config_set — dev-serve is the operator's hand on the file", key)
		}
	}
}

// .
// .
// .
// .
func TestUILayoutLoadValidateWatch(t *testing.T) {
	dir := t.TempDir()
	a := New(&Config{Identity: IdentityConfig{LedgerPath: filepath.Join(dir, "ledger.jsonl")}})

	path := a.uiLayoutPath()
	if path != filepath.Join(dir, "ui-layout.json") {
		t.Fatalf("layout must live in the data dir root, got %s", path)
	}

	// .
	a.loadUILayout(true)
	if a.currentUILayout() != nil {
		t.Fatal("absent file must mean no layout")
	}

	good := `{"v":1,"profiles":{"desktop":{"panel":["hello"]},"mobile":{"dock":["hello"]}}}`
	if err := os.WriteFile(path, []byte(good), 0o644); err != nil {
		t.Fatal(err)
	}
	if !a.loadUILayout(true) {
		t.Fatal("a fresh valid layout must register as changed")
	}
	if string(a.currentUILayout()) != good {
		t.Fatal("the operator's raw bytes are the truth that travels")
	}

	// .
	if err := os.WriteFile(path, []byte(`{"v":1,"profiles":{`), 0o644); err != nil {
		t.Fatal(err)
	}
	if a.loadUILayout(true) || string(a.currentUILayout()) != good {
		t.Fatal("invalid JSON must keep the previous layout")
	}
	// .
	if err := os.WriteFile(path, []byte(`{"v":2,"profiles":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if a.loadUILayout(true) || string(a.currentUILayout()) != good {
		t.Fatal("v!=1 must keep the previous layout")
	}

	// .
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if !a.loadUILayout(true) || a.currentUILayout() != nil {
		t.Fatal("deleting the file must clear the layout")
	}

	// .
	// .
	// .
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a.bgCtx = ctx
	go a.watchUILayout()
	time.Sleep(150 * time.Millisecond)
	if err := os.WriteFile(path, []byte(good), 0o644); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if string(a.currentUILayout()) == good {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("watcher never picked up the layout change")
}

// .
// .
// .
func TestSectionPackageActivationSkipsBrokenLoudly(t *testing.T) {
	dir := t.TempDir()
	result := genesistest.NewRoot(t).Birth(t, genesis.BirthConfig{
		Name:       "SectionTest",
		KeyPath:    filepath.Join(dir, "identity.sec"),
		LedgerPath: filepath.Join(dir, "ledger.jsonl"),
		DBPath:     filepath.Join(dir, "aii.db"),
	})
	result.Ledger.Close()

	badFiles := map[string][]byte{
		"section.json": []byte(`{"id":"bad","slot":"sidebar"}`),
		"index.html":   []byte("<!DOCTYPE html>"),
	}
	bad := writeAssetPkg(t, dir, "org.example.bad", badFiles)
	good := buildSectionPkg(t, dir, "org.example.hello", "hello")
	installPluginDir(t, dir, "org.example.bad", bad)
	installPluginDir(t, dir, "org.example.hello", good)
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
		t.Fatalf("a refused section package must never fail boot: %v", err)
	}
	defer app.Stop()
	if _, ok := app.sections.Get("bad"); ok {
		t.Fatal("the unknown-slot declaration must refuse registration")
	}
	if _, ok := app.sections.Get("hello"); !ok {
		t.Fatal("the good section must activate beside the refused one")
	}
}
