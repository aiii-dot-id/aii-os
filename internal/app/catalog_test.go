package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/aiii-dot-id/aii-os/internal/firewall"
	"github.com/aiii-dot-id/aii-os/internal/plugincatalog"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/genesis"
	"github.com/aiii-dot-id/aii-os/internal/genesis/genesistest"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt/packagetest"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
	"github.com/aiii-dot-id/aii-os/internal/sigenvelope"
)

// catalogInstallDir births an identity in a fresh install dir and returns
// it; the caller builds fixtures before Chdir, then starts the app.
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

// startCatalogApp boots the identity in dir holding cat as its catalog.
// The index reaches the app the one way any index does: a checkout signed
// by a platform_release role the config pins, verified at boot.
func startCatalogApp(t *testing.T, dir string, cat plugincatalog.Index) *App {
	t.Helper()
	catDir, rootPath, _ := signedCatalogDir(t, dir, cat)
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
	t.Cleanup(func() { stopApp(t, app) })
	if app.catalog.Current() == nil {
		t.Fatal("the signed catalog was not held at boot")
	}
	return app
}

// signedCatalogDir writes cat as a catalog checkout under dir, signed by a
// fresh platform_release role, and that role's root envelope beside it.
func signedCatalogDir(t *testing.T, dir string, cat plugincatalog.Index) (catDir, rootPath string, root *sigenvelope.PublicKeyEnvelope) {
	t.Helper()
	plat, err := packagetest.NewRole("test-platform", packagetest.KeyTypePlatformRelease)
	if err != nil {
		t.Fatal(err)
	}
	rootBytes, _ := json.Marshal(plat.Env)
	rootPath = filepath.Join(dir, "platform-root.json")
	if err := os.WriteFile(rootPath, rootBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	catDir = filepath.Join(dir, "catalog")
	if err := os.MkdirAll(catDir, 0o755); err != nil {
		t.Fatal(err)
	}
	block, _ := json.MarshalIndent(cat, "", "  ")
	md := []byte("# Catalog\n\n```json\n" + string(block) + "\n```\n")
	if err := os.WriteFile(filepath.Join(catDir, plugincatalog.CatalogFile), md, 0o644); err != nil {
		t.Fatal(err)
	}
	sig, err := plat.Sign(plugincatalog.ArtifactKindPluginCatalog, map[string]any{
		"catalog_version": cat.Version, "generated": cat.Generated, "catalog_sha256": sigenvelope.SHA256Prefixed(md),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(catDir, plugincatalog.CatalogSigFile), sig, 0o644); err != nil {
		t.Fatal(err)
	}
	return catDir, rootPath, plat.Env
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

// A plugin the catalog names for this host is downloaded, hash-checked,
// installed into plugins/<id>/, and activated by the sweep (S12-C). The
// package is portable ("*"), the WASM case.
func TestInstallFromCatalogActivates(t *testing.T) {
	dir := catalogInstallDir(t)
	pkgBytes, err := os.ReadFile(buildResponderVer(t, dir, "org.example.cat", "1.0.0", ""))
	if err != nil {
		t.Fatal(err)
	}
	url, sha := servePackage(t, pkgBytes)
	t.Chdir(dir)
	app := startCatalogApp(t, dir, plugincatalog.Index{Version: 1, Plugins: []plugincatalog.CatalogEntry{
		{ID: "org.example.cat", Version: "1.0.0", Tier: "T1", Summary: "a cat",
			Packages: []plugincatalog.CatalogPackage{{Platform: "*", Arch: "*", URL: url, SHA256: sha, Size: int64(len(pkgBytes))}}}}})
	app.pkgFetch = loopbackFetcher()

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
	app := startCatalogApp(t, dir, plugincatalog.Index{Version: 1, Plugins: []plugincatalog.CatalogEntry{
		{ID: "org.example.bad", Version: "1.0.0", Tier: "T1", Summary: "bad hash",
			Packages: []plugincatalog.CatalogPackage{{Platform: "*", Arch: "*", URL: url, SHA256: "sha256:0000", Size: int64(len(pkgBytes))}}}}})
	app.pkgFetch = loopbackFetcher()

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
	// A native build for a platform this host is not, and no portable.
	app := startCatalogApp(t, dir, plugincatalog.Index{Version: 1, Plugins: []plugincatalog.CatalogEntry{
		{ID: "org.example.native", Version: "1.0.0", Tier: "T3", Summary: "wrong host",
			Packages: []plugincatalog.CatalogPackage{{Platform: "plan9", Arch: "sparc", URL: "https://x/p.aiiospkg", SHA256: "sha256:aa", Size: 1}}}}})
	app.pkgFetch = loopbackFetcher()

	if err := app.InstallFromCatalog(context.Background(), "org.example.native"); err == nil {
		t.Fatal("a catalog with no build for this host must refuse the install")
	}
}

// The whole config path: a platform-signed catalog on disk, the pinned
// platform_release root in config — the app loads and verifies it.
func TestCatalogLoadsFromConfig(t *testing.T) {
	dir := t.TempDir()
	result := genesistest.NewRoot(t).Birth(t, genesis.BirthConfig{
		Name: "CfgCat", KeyPath: filepath.Join(dir, "identity.sec"),
		LedgerPath: filepath.Join(dir, "ledger.jsonl"), DBPath: filepath.Join(dir, "aii.db"),
	})
	result.Ledger.Close()
	catDir, rootPath, _ := signedCatalogDir(t, dir, plugincatalog.Index{Version: 1, Generated: "2026-09-05T00:00:00Z", Plugins: []plugincatalog.CatalogEntry{
		{ID: "org.example.listed", Version: "2.0.0", Tier: "T1", Summary: "listed",
			Packages: []plugincatalog.CatalogPackage{{Platform: "*", Arch: "*", URL: "https://x/l.aiiospkg", SHA256: "sha256:aa", Size: 1}}}}})
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
	defer stopApp(t, app)
	got := app.catalog.Current()
	if got == nil || len(got.Plugins) != 1 || got.Plugins[0].ID != "org.example.listed" {
		t.Fatalf("the app loads and verifies the configured catalog, got %+v", got)
	}
}

// THE HOST'S CATALOG WIRING, proved from inside app before the index moved
// to internal/plugincatalog: with no checkout configured, the kept copy
// beside the ledger is opened and verified against the pinned root (a
// boot offline); a catalog_url change wakes the refresh loop; and a
// refresh, the operator's included, fetches the configured URL. The URLs
// name a loopback address, so the one egress guard refuses them before
// any request: that typed refusal, with no test seam, is the proof the
// configured URL reached the fetch. A refused refresh leaves the kept
// index held.
func TestTheHostOpensItsKeptCatalogAndRefreshesFromTheConfiguredURL(t *testing.T) {
	dir := catalogInstallDir(t)
	catDir, rootPath, _ := signedCatalogDir(t, dir, plugincatalog.Index{Version: 1, Generated: "2026-09-28T00:00:00Z", Plugins: []plugincatalog.CatalogEntry{
		{ID: "org.example.kept", Version: "1.0.0", Tier: "T1", Summary: "kept",
			Packages: []plugincatalog.CatalogPackage{{Platform: "*", Arch: "*", URL: "https://x/k.aiiospkg", SHA256: "sha256:aa", Size: 1}}}}})
	// A kept copy is the verified pair a refresh writes: a checkout's shape.
	if err := os.Rename(catDir, filepath.Join(dir, "plugins-catalog")); err != nil {
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
		Plugins:    PluginsConfig{Autoload: "T0", PlatformRoot: rootPath, CatalogURL: "https://127.0.0.1:1/aiios-plugins.md"},
		Agency:     defaultConfig().Agency,
	}
	app := New(cfg)
	if err := startLiveForTest(app); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stopApp(t, app) })
	kept := func() bool {
		got := app.catalog.Current()
		return got != nil && len(got.Plugins) == 1 && got.Plugins[0].ID == "org.example.kept"
	}
	if !kept() {
		t.Fatalf("the kept copy beside the ledger is opened and verified at boot, got %+v", app.catalog.Current())
	}

	// A catalog_url change wakes the loop now, well before its first timed
	// wake (15 s after boot): the refusal reaches the view within seconds.
	const moved = "https://127.0.0.1:1/moved.md"
	if _, err := app.applyConfigChange(map[string]any{"plugins.catalog_url": moved}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, _, refusal := app.catalogState(); refusal != "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("a catalog_url change wakes the refresh loop")
		}
		time.Sleep(20 * time.Millisecond)
	}

	// The operator's Refresh: the configured URL, through the guard.
	if err := app.buildLiveHandler().CatalogRefresh(); !errors.Is(err, firewall.ErrEgressBlocked) {
		t.Fatalf("a refresh fetches the configured %s through the egress guard, got %v", moved, err)
	}
	if url, _, refusal := app.catalogState(); url != moved || refusal == "" {
		t.Fatalf("the view names the configured URL and the refusal: url=%q refusal=%q", url, refusal)
	}
	if !kept() {
		t.Fatal("a refused refresh leaves the kept index held")
	}
}

// Install then remove: the plugin activates, then uninstall deletes its
// directory and the sweep deactivates it.
func TestUninstallPlugin(t *testing.T) {
	dir := catalogInstallDir(t)
	pkgBytes, _ := os.ReadFile(buildResponderVer(t, dir, "org.example.rm", "1.0.0", ""))
	url, sha := servePackage(t, pkgBytes)
	t.Chdir(dir)
	app := startCatalogApp(t, dir, plugincatalog.Index{Version: 1, Plugins: []plugincatalog.CatalogEntry{
		{ID: "org.example.rm", Version: "1.0.0", Tier: "T1", Summary: "rm",
			Packages: []plugincatalog.CatalogPackage{{Platform: "*", Arch: "*", URL: url, SHA256: sha, Size: int64(len(pkgBytes))}}}}})
	app.pkgFetch = loopbackFetcher()
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
	for _, bad := range []string{"../evil", "a/b", "..", "", "a/../b", "."} {
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

// catalogViews marks, per offering, whether a build exists for this host
// and whether a release is installed (S12-D).
func TestCatalogViews(t *testing.T) {
	app := &App{}
	dir := t.TempDir()
	catDir, _, root := signedCatalogDir(t, dir, plugincatalog.Index{Plugins: []plugincatalog.CatalogEntry{
		{ID: "org.a", Version: "1.0.0", Tier: "T1", Summary: "a",
			Packages: []plugincatalog.CatalogPackage{{Platform: "*", Arch: "*", URL: "u", SHA256: "s", Size: 1}}},
		{ID: "org.b", Version: "2.0.0", Tier: "T3", Summary: "b",
			Packages: []plugincatalog.CatalogPackage{{Platform: "plan9", Arch: "sparc", URL: "u", SHA256: "s", Size: 1}}},
	}})
	app.catalog.Open(catDir, "", root, nil) // nothing here refreshes, so no URL is read
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

// An installed release the index ranks below its own is offered the
// update — only where a build for this host exists to update to.
func TestCatalogOffersUpdatesForInstalledReleasesItOutranks(t *testing.T) {
	cat := &plugincatalog.Index{Version: 1, Plugins: []plugincatalog.CatalogEntry{
		{ID: "org.example.newer", Version: "0.2.0", Packages: []plugincatalog.CatalogPackage{{Platform: "*", Arch: "*", URL: "https://x/a", SHA256: "sha256:aa", Size: 1}}},
		{ID: "org.example.same", Version: "0.2.0", Packages: []plugincatalog.CatalogPackage{{Platform: "*", Arch: "*", URL: "https://x/b", SHA256: "sha256:bb", Size: 1}}},
		{ID: "org.example.nobuild", Version: "9.0.0", Packages: []plugincatalog.CatalogPackage{{Platform: "plan9", Arch: "mips", URL: "https://x/c", SHA256: "sha256:cc", Size: 1}}},
		{ID: "org.example.absent", Version: "1.0.0", Packages: []plugincatalog.CatalogPackage{{Platform: "*", Arch: "*", URL: "https://x/d", SHA256: "sha256:dd", Size: 1}}},
	}}
	views := catalogViewsFor(cat, map[string]installedRelease{"org.example.newer": {version: "0.1.0"}, "org.example.same": {version: "0.2.0"}, "org.example.nobuild": {version: "0.1.0"}}, nil)
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

func TestCatalogOffersPrereleaseUpgradesTheIndexOutranks(t *testing.T) {
	pkgs := []plugincatalog.CatalogPackage{{Platform: "*", Arch: "*", URL: "https://x/v", SHA256: "sha256:vv", Size: 1}}
	cat := &plugincatalog.Index{Version: 1, Plugins: []plugincatalog.CatalogEntry{
		{ID: "id.aiii.voice", Version: "0.1.0-beta.3", Packages: pkgs},
		{ID: "org.example.tenth", Version: "0.1.0-beta.10", Packages: pkgs},
		{ID: "org.example.backwards", Version: "0.1.0-beta.1", Packages: pkgs},
		{ID: "org.example.released", Version: "0.1.0", Packages: pkgs},
		{ID: "org.example.regressing", Version: "0.1.0-beta.3", Packages: pkgs},
	}}
	installed := map[string]installedRelease{
		"id.aiii.voice":          {version: "0.1.0-beta.1"}, // the real case
		"org.example.tenth":      {version: "0.1.0-beta.9"}, // numeric, not lexical
		"org.example.backwards":  {version: "0.1.0-beta.3"}, // older is not an update
		"org.example.released":   {version: "0.1.0-beta.3"}, // a release outranks its prereleases
		"org.example.regressing": {version: "0.1.0"},        // and a prerelease does not outrank one
	}
	want := map[string]bool{"id.aiii.voice": true, "org.example.tenth": true,
		"org.example.backwards": false, "org.example.released": true, "org.example.regressing": false}
	views := catalogViewsFor(cat, installed, nil)
	for _, v := range views {
		if v.UpdateAvailable != want[v.ID] {
			t.Errorf("%s: installed %s, catalog %s -> update_available=%v want %v",
				v.ID, installed[v.ID].version, v.Version, v.UpdateAvailable, want[v.ID])
		}
	}
	if n := countUpdates(views); n != 3 {
		t.Fatalf("the badge counts the three real upgrades: %d", n)
	}
}

// assertNoInstallResidue: no slot for id, and nothing downloading beside
// the slots.
func assertNoInstallResidue(t *testing.T, dir, id string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(dir, "plugins", id)); !os.IsNotExist(err) {
		t.Fatalf("a refused install left plugins/%s behind (%v)", id, err)
	}
	if m, _ := filepath.Glob(filepath.Join(dir, "plugins", ".download-*")); len(m) != 0 {
		t.Fatalf("a refused install left its download behind: %v", m)
	}
}

// A refused first install leaves no plugins/<id> — the directory is made
// only for a package that has passed the catalog's checks — and every
// refusal names the requirement it failed.
func TestCatalogInstallRefusalsAreTypedAndLeaveNothing(t *testing.T) {
	dir := t.TempDir()
	pkgBytes := []byte("bytes the refusals turn away before anything reads them")
	url, sha := servePackage(t, pkgBytes)
	size := int64(len(pkgBytes))
	portable := func(url, sha string, size int64) []plugincatalog.CatalogPackage {
		return []plugincatalog.CatalogPackage{{Platform: "*", Arch: "*", URL: url, SHA256: sha, Size: size}}
	}
	catDir, _, root := signedCatalogDir(t, dir, plugincatalog.Index{Version: 1, Plugins: []plugincatalog.CatalogEntry{
		{ID: "org.example.hash", Version: "1.0.0", Tier: "T1", Packages: portable(url, "sha256:0000", size)},
		{ID: "org.example.size", Version: "1.0.0", Tier: "T1", Packages: portable(url, sha, size+1)},
		{ID: "org.example.fetch", Version: "1.0.0", Tier: "T1", Packages: portable(url+"/broken", sha, size)},
		{ID: "org.example.native", Version: "1.0.0", Tier: "T3", Packages: []plugincatalog.CatalogPackage{{Platform: "plan9", Arch: "sparc", URL: url, SHA256: sha, Size: size}}},
		{ID: "../escape", Version: "1.0.0", Tier: "T1", Packages: portable(url, sha, size)},
	}})
	app := &App{}
	app.catalog.Open(catDir, "", root, nil)
	reset := errors.New("connection reset mid-body")
	loop := loopbackFetcher()
	app.pkgFetch = func(ctx context.Context, u string, w io.Writer) (int64, error) {
		if strings.HasSuffix(u, "/broken") {
			n, _ := w.Write(pkgBytes[:8])
			return int64(n), reset
		}
		return loop(ctx, u, w)
	}
	t.Chdir(dir)

	for _, tc := range []struct{ id, reason string }{
		{"org.example.hash", "hash"},
		{"org.example.size", "size"},
		{"org.example.fetch", "fetch"},
		{"org.example.native", "not_offered"},
		{"org.example.absent", "not_offered"},
		{"../escape", "not_plain"},
	} {
		err := app.InstallFromCatalog(context.Background(), tc.id)
		var r *CatalogInstallRefusal
		if !errors.As(err, &r) || r.Reason != tc.reason || r.ID != tc.id || r.Cleanup != nil {
			t.Fatalf("%s: want a clean %s refusal, got %#v (%v)", tc.id, tc.reason, r, err)
		}
		assertNoInstallResidue(t, dir, tc.id)
	}
	if err := app.InstallFromCatalog(context.Background(), "org.example.fetch"); !errors.Is(err, reset) {
		t.Fatalf("a fetch refusal carries the download's own error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "escape")); !os.IsNotExist(err) {
		t.Fatalf("an id that is not a plain name reached outside plugins/: %v", err)
	}
	var r *CatalogInstallRefusal
	if err := (&App{}).InstallFromCatalog(context.Background(), "org.example.hash"); !errors.As(err, &r) || r.Reason != "no_catalog" {
		t.Fatalf("with no catalog held the install is refused as such: %v", err)
	}
}

// The package arrives under a temporary name beside the slots and is
// published whole: while it downloads there is no slot for a first
// install and no new package in the slot of an update, and afterwards
// the slot holds exactly the verified bytes — the release it replaces
// removed, no download left over.
func TestCatalogInstallPublishesOnlyAVerifiedPackage(t *testing.T) {
	dir := t.TempDir()
	pkgBytes := []byte("the verified package")
	url, sha := servePackage(t, pkgBytes)
	portable := func(u string) []plugincatalog.CatalogPackage {
		return []plugincatalog.CatalogPackage{{Platform: "*", Arch: "*", URL: u, SHA256: sha, Size: int64(len(pkgBytes))}}
	}
	catDir, _, root := signedCatalogDir(t, dir, plugincatalog.Index{Version: 1, Plugins: []plugincatalog.CatalogEntry{
		{ID: "org.example.fresh", Version: "1.0.0", Tier: "T1", Packages: portable(url + "/fresh")},
		{ID: "org.example.upgrade", Version: "2.0.0", Tier: "T1", Packages: portable(url + "/upgrade")},
	}})
	app := &App{}
	app.catalog.Open(catDir, "", root, nil)
	t.Chdir(dir)
	upgradeSlot := filepath.Join("plugins", "org.example.upgrade")
	if err := os.MkdirAll(upgradeSlot, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(upgradeSlot, "org.example.upgrade-1.0.0.aiiospkg"), []byte("the running release"), 0o640); err != nil {
		t.Fatal(err)
	}
	slotAt := map[string][]string{} // the slot's entries while its package downloads
	loop := loopbackFetcher()
	app.pkgFetch = func(ctx context.Context, u string, w io.Writer) (int64, error) {
		id := "org.example." + u[strings.LastIndex(u, "/")+1:]
		var names []string // nil: no slot
		entries, err := os.ReadDir(filepath.Join("plugins", id))
		switch {
		case err == nil:
			names = []string{}
			for _, e := range entries {
				names = append(names, e.Name())
			}
		case !os.IsNotExist(err):
			t.Errorf("reading the slot: %v", err)
		}
		slotAt[id] = names
		return loop(ctx, u, w)
	}

	for _, tc := range []struct {
		id, file string
		during   []string // nil: no slot at all
	}{
		{"org.example.fresh", "org.example.fresh-1.0.0.aiiospkg", nil},
		{"org.example.upgrade", "org.example.upgrade-2.0.0.aiiospkg", []string{"org.example.upgrade-1.0.0.aiiospkg"}},
	} {
		if err := app.InstallFromCatalog(context.Background(), tc.id); err != nil {
			t.Fatalf("%s: %v", tc.id, err)
		}
		if got := slotAt[tc.id]; (got == nil) != (tc.during == nil) || strings.Join(got, ",") != strings.Join(tc.during, ",") {
			t.Fatalf("%s: while downloading, slot present=%v holding %v; want present=%v holding %v", tc.id, got != nil, got, tc.during != nil, tc.during)
		}
		entries, err := os.ReadDir(filepath.Join("plugins", tc.id))
		if err != nil || len(entries) != 1 || entries[0].Name() != tc.file {
			t.Fatalf("%s: the slot holds exactly %s, got %v (%v)", tc.id, tc.file, entries, err)
		}
		if b, err := os.ReadFile(filepath.Join("plugins", tc.id, tc.file)); err != nil || string(b) != string(pkgBytes) {
			t.Fatalf("%s: the slot holds the verified bytes, got %q (%v)", tc.id, b, err)
		}
		if m, _ := filepath.Glob(filepath.Join("plugins", ".download-*")); len(m) != 0 {
			t.Fatalf("%s: a download was left beside the slots: %v", tc.id, m)
		}
	}
}

// Shutdown cancels a download in flight: the dashboard's install runs on
// the application's lifecycle context as the application's own work, so
// shutdown both cancels it and joins it, and the refused install leaves
// nothing behind.
func TestShutdownCancelsACatalogDownloadInFlight(t *testing.T) {
	dir := catalogInstallDir(t)
	body := make([]byte, 1<<20)
	sum := sha256.Sum256(body)
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		_, _ = w.Write(body[:4096])
		w.(http.Flusher).Flush()
		once.Do(func() { close(started) })
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	t.Cleanup(srv.Close)
	t.Chdir(dir)
	const id = "org.example.slow"
	app := startCatalogApp(t, dir, plugincatalog.Index{Version: 1, Plugins: []plugincatalog.CatalogEntry{
		{ID: id, Version: "1.0.0", Tier: "T1", Summary: "slow",
			Packages: []plugincatalog.CatalogPackage{{Platform: "*", Arch: "*", URL: srv.URL, SHA256: "sha256:" + hex.EncodeToString(sum[:]), Size: int64(len(body))}}}}})
	t.Cleanup(func() { close(release) }) // runs before app.Stop: a download shutdown missed ends here
	app.pkgFetch = loopbackFetcher()

	// The dashboard's path: its install action, run as the application's
	// background work.
	act := app.buildLiveHandler().PluginAct
	done := make(chan error, 1)
	if !app.runBackground(func() { done <- act(dashboard.PluginAction{Action: "install", ID: id}) }) {
		t.Fatal("the application refused the install before shutdown")
	}
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("the download never started")
	}
	if _, err := os.Stat(filepath.Join(dir, "plugins", id)); !os.IsNotExist(err) {
		t.Fatalf("a slot exists while its package is still downloading: %v", err)
	}
	if m, _ := filepath.Glob(filepath.Join(dir, "plugins", ".download-*")); len(m) != 1 {
		t.Fatalf("the download is not arriving beside the slots: %v", m)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := app.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown did not cancel and join the download in flight: %v", err)
	}
	select {
	case err := <-done:
		var r *CatalogInstallRefusal
		if !errors.As(err, &r) || r.Reason != "fetch" || r.Cleanup != nil || !errors.Is(err, context.Canceled) {
			t.Fatalf("a cancelled download is a clean fetch refusal naming the cancellation, got %v", err)
		}
	default:
		t.Fatal("shutdown returned before the install it owns")
	}
	assertNoInstallResidue(t, dir, id)
}

// The plugins view carries, for a release the host runs, the tier it
// verified — beside the index's claim, which is all an offering has.
func TestCatalogViewsCarryTheVerifiedTierBesideTheIndexClaim(t *testing.T) {
	app := &App{}
	dir := t.TempDir()
	portable := []plugincatalog.CatalogPackage{{Platform: "*", Arch: "*", URL: "u", SHA256: "s", Size: 1}}
	catDir, _, root := signedCatalogDir(t, dir, plugincatalog.Index{Plugins: []plugincatalog.CatalogEntry{
		{ID: "org.example.run", Version: "1.0.0", Tier: "T3", Summary: "runs", Packages: portable},
		{ID: "org.example.offer", Version: "1.0.0", Tier: "T2", Summary: "offered", Packages: portable},
	}})
	app.catalog.Open(catDir, "", root, nil)
	app.pluginMu.Lock()
	app.plugins = []*pluginhost.ActivePlugin{{ID: "org.example.run", Version: "1.0.0", Tier: packagefmt.TierT1}}
	app.pluginMu.Unlock()

	views := app.catalogViews()
	got := map[string][2]string{}
	for _, v := range views {
		got[v.ID] = [2]string{v.Tier, v.InstalledTier}
	}
	if got["org.example.run"] != [2]string{"T3", "T1"} || got["org.example.offer"] != [2]string{"T2", ""} {
		t.Fatalf("claim and verified tier per row: %v", got)
	}
	b, err := json.Marshal(views)
	if err != nil || strings.Count(string(b), `"installed_tier":"T1"`) != 1 || strings.Count(string(b), `"installed_tier"`) != 1 {
		t.Fatalf("the page reads the verified tier as installed_tier, on the installed row only: %s (%v)", b, err)
	}
}

// A HAND EDIT OF THE CATALOG'S ADDRESS WAKES THE REFRESH TOO. The door
// pokes the loop; a reload swapped the address in and left the loop asleep
// until its timed wake, hours away after the first.
func TestAReloadOfTheCatalogAddressWakesTheRefresh(t *testing.T) {
	dir := catalogInstallDir(t)
	catDir, rootPath, _ := signedCatalogDir(t, dir, plugincatalog.Index{Version: 1, Generated: "2026-09-28T00:00:00Z", Plugins: []plugincatalog.CatalogEntry{
		{ID: "org.example.kept", Version: "1.0.0", Tier: "T1", Summary: "kept",
			Packages: []plugincatalog.CatalogPackage{{Platform: "*", Arch: "*", URL: "https://x/k.aiiospkg", SHA256: "sha256:aa", Size: 1}}}}})
	if err := os.Rename(catDir, filepath.Join(dir, "plugins-catalog")); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	// A whole config, as a file a reload reads must be.
	cfg := defaultConfig()
	cfg.Identity.KeyPath = filepath.Join(dir, "identity.sec")
	cfg.Identity.LedgerPath = filepath.Join(dir, "ledger.jsonl")
	cfg.Identity.DBPath = filepath.Join(dir, "aii.db")
	cfg.LLM = withTestProvider(t, dir, "test", "https://127.0.0.1:1", "m", "sk-x")
	cfg.SourcePath = filepath.Join(dir, "config.json")
	cfg.Dashboard.Port = 0
	cfg.Tools.CWD = dir
	cfg.Plugins = PluginsConfig{Autoload: "T0", PlatformRoot: rootPath, CatalogURL: "https://127.0.0.1:1/aiios-plugins.md"}
	app := New(cfg)
	if err := startLiveForTest(app); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stopApp(t, app) })

	const moved = "https://127.0.0.1:1/moved.md"
	edited := app.configSnapshot()
	edited.Plugins.CatalogURL = moved
	if _, err := saveConfig(&edited); err != nil {
		t.Fatal(err)
	}
	app.reloadConfig()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if url, _, refusal := app.catalogState(); url == moved && refusal != "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("a reloaded catalog_url did not wake the refresh loop")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
