package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/genesis"
	"github.com/aiii-dot-id/aii-os/internal/genesis/genesistest"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt/packagetest"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
	"github.com/aiii-dot-id/aii-os/internal/sigenvelope"
)

// .
// .
func catalogInstallDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	result := genesistest.NewRoot(t).Birth(t, genesis.BirthConfig{
		Name: "CatTest", KeyPath: filepath.Join(dir, "identity.sec"),
		LedgerPath: filepath.Join(dir, "ledger.jsonl"), DBPath: filepath.Join(dir, "aii.db"),
	})
	result.Ledger.Close()
	return dir
}

func loopbackFetcher() packageFetch {
	return func(ctx context.Context, url string, w io.Writer) (int64, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return 0, err
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return 0, err
		}
		defer resp.Body.Close()
		return io.Copy(w, resp.Body)
	}
}

func startCatalogApp(t *testing.T, dir string) *App {
	t.Helper()
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
	t.Cleanup(app.Stop)
	return app
}

func setCatalog(app *App, cat pluginhost.Catalog) {
	app.catalogMu.Lock()
	app.catalog = &cat
	app.catalogMu.Unlock()
}

func servePackage(t *testing.T, bytes []byte) (url, sha string) {
	t.Helper()
	sum := sha256.Sum256(bytes)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(bytes)
	}))
	t.Cleanup(srv.Close)
	return srv.URL, "sha256:" + hex.EncodeToString(sum[:])
}

// .
// .
// .
func TestInstallFromCatalogActivates(t *testing.T) {
	dir := catalogInstallDir(t)
	pkgBytes, err := os.ReadFile(buildResponderVer(t, dir, "org.example.cat", "1.0.0", ""))
	if err != nil {
		t.Fatal(err)
	}
	url, sha := servePackage(t, pkgBytes)
	t.Chdir(dir)
	app := startCatalogApp(t, dir)
	app.pkgFetch = loopbackFetcher()
	setCatalog(app, pluginhost.Catalog{Version: 1, Plugins: []pluginhost.CatalogEntry{
		{ID: "org.example.cat", Version: "1.0.0", Tier: "T1", Summary: "a cat",
			Packages: []pluginhost.CatalogPackage{{Platform: "*", Arch: "*", URL: url, SHA256: sha, Size: int64(len(pkgBytes))}}}}})

	if err := app.InstallFromCatalog(context.Background(), "org.example.cat"); err != nil {
		t.Fatalf("install: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "plugins", "org.example.cat", "org.example.cat-1.0.0.aiiospkg")); err != nil {
		t.Fatalf("the package must land in the slot: %v", err)
	}
	const tool = "pl_org_example_cat_ping"
	deadline := time.Now().Add(20 * time.Second)
	for {
		if _, ok := app.toolReg.Get(tool); ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the installed plugin did not activate; tools: %v", app.toolReg.Names())
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func TestInstallRefusesHashMismatch(t *testing.T) {
	dir := catalogInstallDir(t)
	pkgBytes, _ := os.ReadFile(buildResponderVer(t, dir, "org.example.bad", "1.0.0", ""))
	url, _ := servePackage(t, pkgBytes)
	t.Chdir(dir)
	app := startCatalogApp(t, dir)
	app.pkgFetch = loopbackFetcher()
	setCatalog(app, pluginhost.Catalog{Version: 1, Plugins: []pluginhost.CatalogEntry{
		{ID: "org.example.bad", Version: "1.0.0", Tier: "T1", Summary: "bad hash",
			Packages: []pluginhost.CatalogPackage{{Platform: "*", Arch: "*", URL: url, SHA256: "sha256:0000", Size: int64(len(pkgBytes))}}}}})

	if err := app.InstallFromCatalog(context.Background(), "org.example.bad"); err == nil {
		t.Fatal("a hash mismatch must refuse the install")
	}
	if m, _ := filepath.Glob(filepath.Join(dir, "plugins", "org.example.bad", "*.aiiospkg")); len(m) != 0 {
		t.Fatalf("a refused install leaves no package: %v", m)
	}
}

func TestInstallRefusesWrongPlatform(t *testing.T) {
	dir := catalogInstallDir(t)
	t.Chdir(dir)
	app := startCatalogApp(t, dir)
	app.pkgFetch = loopbackFetcher()
	// .
	setCatalog(app, pluginhost.Catalog{Version: 1, Plugins: []pluginhost.CatalogEntry{
		{ID: "org.example.native", Version: "1.0.0", Tier: "T3", Summary: "wrong host",
			Packages: []pluginhost.CatalogPackage{{Platform: "plan9", Arch: "sparc", URL: "https://x/p.aiiospkg", SHA256: "sha256:aa", Size: 1}}}}})

	if err := app.InstallFromCatalog(context.Background(), "org.example.native"); err == nil {
		t.Fatal("a catalog with no build for this host must refuse the install")
	}
}

// .
// .
func TestCatalogLoadsFromConfig(t *testing.T) {
	dir := t.TempDir()
	result := genesistest.NewRoot(t).Birth(t, genesis.BirthConfig{
		Name: "CfgCat", KeyPath: filepath.Join(dir, "identity.sec"),
		LedgerPath: filepath.Join(dir, "ledger.jsonl"), DBPath: filepath.Join(dir, "aii.db"),
	})
	result.Ledger.Close()
	plat, err := packagetest.NewRole("test-platform", packagetest.KeyTypePlatformRelease)
	if err != nil {
		t.Fatal(err)
	}
	rootBytes, _ := json.Marshal(plat.Env)
	rootPath := filepath.Join(dir, "platform-root.json")
	if err := os.WriteFile(rootPath, rootBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	catDir := filepath.Join(dir, "catalog")
	if err := os.MkdirAll(catDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cat := pluginhost.Catalog{Version: 1, Generated: "2026-09-05T00:00:00Z", Plugins: []pluginhost.CatalogEntry{
		{ID: "org.example.listed", Version: "2.0.0", Tier: "T1", Summary: "listed",
			Packages: []pluginhost.CatalogPackage{{Platform: "*", Arch: "*", URL: "https://x/l.aiiospkg", SHA256: "sha256:aa", Size: 1}}}}}
	block, _ := json.MarshalIndent(cat, "", "  ")
	md := []byte("# Catalog\n\n```json\n" + string(block) + "\n```\n")
	if err := os.WriteFile(filepath.Join(catDir, pluginhost.CatalogFile), md, 0o644); err != nil {
		t.Fatal(err)
	}
	sig, err := plat.Sign(pluginhost.ArtifactKindPluginCatalog, map[string]interface{}{
		"catalog_version": 1, "generated": "2026-09-05T00:00:00Z", "catalog_sha256": sigenvelope.SHA256Prefixed(md),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(catDir, pluginhost.CatalogSigFile), sig, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	cfg := &Config{
		Identity: IdentityConfig{KeyPath: filepath.Join(dir, "identity.sec"),
			LedgerPath: filepath.Join(dir, "ledger.jsonl"), DBPath: filepath.Join(dir, "aii.db")},
		LLM:        withTestProvider(t, dir, "test", "https://127.0.0.1:1", "m", "sk-x"),
		SourcePath: filepath.Join(dir, "config.json"),
		Dashboard:  DashboardConfig{Port: 0},
		Tools:      ToolsConfig{CWD: dir},
		Plugins:    PluginsConfig{Autoload: "T0", PlatformRoot: rootPath, CatalogDir: catDir},
		Agency:     defaultConfig().Agency,
	}
	app := New(cfg)
	if err := startLiveForTest(app); err != nil {
		t.Fatal(err)
	}
	defer app.Stop()
	got := app.Catalog()
	if got == nil || len(got.Plugins) != 1 || got.Plugins[0].ID != "org.example.listed" {
		t.Fatalf("the app loads and verifies the configured catalog, got %+v", got)
	}
}

// .
// .
func TestUninstallPlugin(t *testing.T) {
	dir := catalogInstallDir(t)
	pkgBytes, _ := os.ReadFile(buildResponderVer(t, dir, "org.example.rm", "1.0.0", ""))
	url, sha := servePackage(t, pkgBytes)
	t.Chdir(dir)
	app := startCatalogApp(t, dir)
	app.pkgFetch = loopbackFetcher()
	setCatalog(app, pluginhost.Catalog{Version: 1, Plugins: []pluginhost.CatalogEntry{
		{ID: "org.example.rm", Version: "1.0.0", Tier: "T1", Summary: "rm",
			Packages: []pluginhost.CatalogPackage{{Platform: "*", Arch: "*", URL: url, SHA256: sha, Size: int64(len(pkgBytes))}}}}})
	if err := app.InstallFromCatalog(context.Background(), "org.example.rm"); err != nil {
		t.Fatal(err)
	}
	const tool = "pl_org_example_rm_ping"
	waitUntil(t, func() bool { _, ok := app.toolReg.Get(tool); return ok }, "activate")

	if err := app.UninstallPlugin("org.example.rm"); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	waitUntil(t, func() bool { _, ok := app.toolReg.Get(tool); return !ok }, "deactivate")
	if _, err := os.Stat(filepath.Join(dir, "plugins", "org.example.rm")); !os.IsNotExist(err) {
		t.Fatalf("uninstall removes the directory: %v", err)
	}
}

func TestUninstallRefusesPathTraversal(t *testing.T) {
	app := &App{}
	for _, bad := range []string{"../evil", "a/b", "..", "", "a/../b"} {
		if err := app.UninstallPlugin(bad); err == nil {
			t.Fatalf("uninstall must refuse %q", bad)
		}
	}
}

func waitUntil(t *testing.T, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		if cond() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("condition %q not met within the sweep window", what)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// .
// .
func TestCatalogViews(t *testing.T) {
	app := &App{}
	setCatalog(app, pluginhost.Catalog{Plugins: []pluginhost.CatalogEntry{
		{ID: "org.a", Version: "1.0.0", Tier: "T1", Summary: "a",
			Packages: []pluginhost.CatalogPackage{{Platform: "*", Arch: "*", URL: "u", SHA256: "s", Size: 1}}},
		{ID: "org.b", Version: "2.0.0", Tier: "T3", Summary: "b",
			Packages: []pluginhost.CatalogPackage{{Platform: "plan9", Arch: "sparc", URL: "u", SHA256: "s", Size: 1}}},
	}})
	app.pluginMu.Lock()
	app.plugins = []*pluginhost.ActivePlugin{{ID: "org.a", Version: "0.9.0"}}
	app.pluginMu.Unlock()

	views := app.catalogViews()
	byID := map[string]struct {
		avail, inst bool
		iv          string
	}{}
	for _, v := range views {
		byID[v.ID] = struct {
			avail, inst bool
			iv          string
		}{v.Available, v.Installed, v.InstalledVersion}
	}
	if a := byID["org.a"]; !a.avail || !a.inst || a.iv != "0.9.0" {
		t.Fatalf("org.a: portable build available and installed at 0.9.0, got %+v", a)
	}
	if b := byID["org.b"]; b.avail || b.inst {
		t.Fatalf("org.b: no build for this host, not installed, got %+v", b)
	}
}

// .
// .
// .
// .
func TestCatalogRefreshesFromItsURLAndKeepsTheLastGoodIndex(t *testing.T) {
	dir := t.TempDir()
	result := genesistest.NewRoot(t).Birth(t, genesis.BirthConfig{
		Name: "UrlCat", KeyPath: filepath.Join(dir, "identity.sec"),
		LedgerPath: filepath.Join(dir, "ledger.jsonl"), DBPath: filepath.Join(dir, "aii.db"),
	})
	result.Ledger.Close()
	plat, err := packagetest.NewRole("test-platform", packagetest.KeyTypePlatformRelease)
	if err != nil {
		t.Fatal(err)
	}
	rootBytes, _ := json.Marshal(plat.Env)
	rootPath := filepath.Join(dir, "platform-root.json")
	if err := os.WriteFile(rootPath, rootBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	signed := func(version string) (md, sig []byte) {
		cat := pluginhost.Catalog{Version: 1, Generated: "2026-09-09T00:00:00Z", Plugins: []pluginhost.CatalogEntry{
			{ID: "org.example.listed", Version: version, Tier: "T1", Summary: "listed",
				Packages: []pluginhost.CatalogPackage{{Platform: "*", Arch: "*", URL: "https://x/l.aiiospkg", SHA256: "sha256:aa", Size: 1}}}}}
		block, _ := json.MarshalIndent(cat, "", "  ")
		md = []byte("# Catalog\n\n```json\n" + string(block) + "\n```\n")
		sig, err := plat.Sign(pluginhost.ArtifactKindPluginCatalog, map[string]interface{}{
			"catalog_version": 1, "generated": "2026-09-09T00:00:00Z", "catalog_sha256": sigenvelope.SHA256Prefixed(md),
		})
		if err != nil {
			t.Fatal(err)
		}
		return md, sig
	}
	t.Chdir(dir)
	const url = "https://catalog.example.test/aiios-plugins.md"
	cfg := &Config{
		Identity: IdentityConfig{KeyPath: filepath.Join(dir, "identity.sec"),
			LedgerPath: filepath.Join(dir, "ledger.jsonl"), DBPath: filepath.Join(dir, "aii.db")},
		LLM:        withTestProvider(t, dir, "test", "https://127.0.0.1:1", "m", "sk-x"),
		SourcePath: filepath.Join(dir, "config.json"),
		Dashboard:  DashboardConfig{Port: 0},
		Tools:      ToolsConfig{CWD: dir},
		Plugins:    PluginsConfig{Autoload: "T0", PlatformRoot: rootPath, CatalogURL: url},
		Agency:     defaultConfig().Agency,
	}
	app := New(cfg)
	// .
	// .
	// .
	app.catalogFetch = func(context.Context, string, int64) ([]byte, error) {
		return nil, fmt.Errorf("catalog fixture is not enabled")
	}
	if err := startLiveForTest(app); err != nil {
		t.Fatal(err)
	}
	app.Stop()
	if app.Catalog() != nil {
		t.Fatal("nothing is loaded before a fetch verifies")
	}
	md, sig := signed("2.0.0")
	served := map[string][]byte{url: md, url + ".sig": sig}
	app.catalogFetch = func(ctx context.Context, u string, max int64) ([]byte, error) {
		b, ok := served[u]
		if !ok {
			return nil, fmt.Errorf("no such artifact %s", u)
		}
		return b, nil
	}
	if err := app.refreshCatalog(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := app.Catalog(); got == nil || len(got.Plugins) != 1 || got.Plugins[0].Version != "2.0.0" {
		t.Fatalf("the verified fetch is adopted: %+v", got)
	}
	if _, at, refusal := app.catalogState(); at == "" || refusal != "" {
		t.Fatalf("the refresh is recorded: at=%q refusal=%q", at, refusal)
	}
	kept, err := pluginhost.LoadCatalog(app.catalogCache, plat.Env)
	if err != nil || kept.Plugins[0].Version != "2.0.0" {
		t.Fatalf("the verified pair is kept beside the data: %v", err)
	}

	// .
	// .
	served[url] = []byte("# Catalog!\n" + string(md[len("# Catalog\n"):]))
	if err := app.refreshCatalog(context.Background()); err == nil {
		t.Fatal("a fetch the signature does not cover is refused")
	}
	if got := app.Catalog(); got == nil || got.Plugins[0].Version != "2.0.0" {
		t.Fatal("the last good index stays")
	}
	if _, _, refusal := app.catalogState(); refusal == "" {
		t.Fatal("the refusal is reported to the view")
	}
	// .
	delete(served, url)
	if err := app.refreshCatalog(context.Background()); err == nil || app.Catalog().Plugins[0].Version != "2.0.0" {
		t.Fatalf("an index that does not arrive changes nothing: %v", err)
	}
	// .
	served[url], served[url+".sig"] = signed("2.1.0")
	if err := app.refreshCatalog(context.Background()); err != nil || app.Catalog().Plugins[0].Version != "2.1.0" {
		t.Fatalf("a verified fetch replaces the index: %v", err)
	}
}

// .
// .
func TestCatalogOffersUpdatesForInstalledReleasesItOutranks(t *testing.T) {
	cat := &pluginhost.Catalog{Version: 1, Plugins: []pluginhost.CatalogEntry{
		{ID: "org.example.newer", Version: "0.2.0", Packages: []pluginhost.CatalogPackage{{Platform: "*", Arch: "*", URL: "https://x/a", SHA256: "sha256:aa", Size: 1}}},
		{ID: "org.example.same", Version: "0.2.0", Packages: []pluginhost.CatalogPackage{{Platform: "*", Arch: "*", URL: "https://x/b", SHA256: "sha256:bb", Size: 1}}},
		{ID: "org.example.nobuild", Version: "9.0.0", Packages: []pluginhost.CatalogPackage{{Platform: "plan9", Arch: "mips", URL: "https://x/c", SHA256: "sha256:cc", Size: 1}}},
		{ID: "org.example.absent", Version: "1.0.0", Packages: []pluginhost.CatalogPackage{{Platform: "*", Arch: "*", URL: "https://x/d", SHA256: "sha256:dd", Size: 1}}},
	}}
	views := catalogViewsFor(cat, map[string]string{"org.example.newer": "0.1.0", "org.example.same": "0.2.0", "org.example.nobuild": "0.1.0"})
	want := map[string]bool{"org.example.newer": true, "org.example.same": false, "org.example.nobuild": false, "org.example.absent": false}
	for _, v := range views {
		if v.UpdateAvailable != want[v.ID] {
			t.Errorf("%s: update_available=%v want %v (%+v)", v.ID, v.UpdateAvailable, want[v.ID], v)
		}
	}
	if countUpdates(views) != 1 {
		t.Fatalf("the badge counts one: %d", countUpdates(views))
	}
}
