package app

// AN ASSET PACKAGE IS INSTALLED (step 16b, R14-12). A package that serves a
// page, or files the host reads, runs no program of its own. Its row in the
// installed list, the catalog's mark and the stored list all say it is
// installed, with what its verified manifest says of it and nothing of a
// plugin's: no mode, component set, operations or settings.

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/genesis"
	"github.com/aiii-dot-id/aii-os/internal/genesis/genesistest"
	"github.com/aiii-dot-id/aii-os/internal/plugincatalog"
)

func TestAnAssetPackageIsInstalledAndListedAsWhatItIs(t *testing.T) {
	dir := t.TempDir()
	result := genesistest.NewRoot(t).Birth(t, genesis.BirthConfig{
		Name:       "AssetRow",
		KeyPath:    filepath.Join(dir, "identity.sec"),
		LedgerPath: filepath.Join(dir, "ledger.jsonl"),
		DBPath:     filepath.Join(dir, "aii.db"),
	})
	result.Ledger.Close()

	page := writeAssetPkgSaying(t, dir, "org.example.hello", sectionPkgFiles("hello"),
		map[string]string{"title": "Hello page", "description": "A page that says hello.", "publisher": "Example Publisher"})
	plain := writeAssetPkg(t, dir, "org.example.prompts", map[string][]byte{"prompts/hi.md": []byte("# hi\n")})
	plugin := buildResponderPkg(t, dir, "org.example.good")
	installPluginDir(t, dir, "org.example.hello", page)
	installPluginDir(t, dir, "org.example.prompts", plain)
	installPluginDir(t, dir, "org.example.good", plugin)
	t.Chdir(dir)
	// A dev section beside them is no package: it gets no row.
	devDir := filepath.Join(dir, "devsec")
	if err := os.MkdirAll(devDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for rel, body := range map[string]string{"section.json": `{"id":"devsec","slot":"rail"}`, "index.html": "<!DOCTYPE html>dev"} {
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
		Plugins:    PluginsConfig{Autoload: "T0", DevSection: &DevSectionConfig{ID: "devsec", Path: devDir}},
		Agency:     defaultConfig().Agency,
	}
	app := New(cfg)
	if err := startLiveForTest(app); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer stopApp(t, app)
	for _, id := range []string{"hello", "devsec", "status"} {
		if _, ok := app.sections.Get(id); !ok {
			t.Fatalf("rig: section %q did not register", id)
		}
	}

	// THE ROWS: one per package, by id, each saying what kind it is; a
	// built-in or dev section is no package and has none.
	rows := map[string]dashboard.PluginView{}
	var order []string
	for _, v := range app.pluginViews(app.cfg) {
		rows[v.ID] = v
		order = append(order, v.ID)
	}
	if !sort.StringsAreSorted(order) || len(order) != 3 {
		t.Fatalf("the installed list is not every package once, by id: %v", order)
	}
	pg, ok := rows["org.example.hello"]
	if !ok {
		t.Fatalf("a package that serves a page has no row: %v", order)
	}
	if pg.Kind != "asset" || pg.Version != "0.1.0" || pg.Tier == "" || pg.PackageHash == "" ||
		pg.Title != "Hello page" || pg.Description != "A page that says hello." || pg.Publisher != "Example Publisher" {
		t.Fatalf("the page's row does not say what its manifest says: %+v", pg)
	}
	if pg.Mode != "" || pg.Variant != "" || len(pg.Tools) != 0 || len(pg.Operations) != 0 || len(pg.Settings) != 0 || len(pg.Acts) != 0 {
		t.Fatalf("the page's row was given a plugin's: %+v", pg)
	}
	if k := rows["org.example.prompts"].Kind; k != "asset" {
		t.Fatalf("an asset that serves no page: kind %q", k)
	}
	if k := rows["org.example.good"].Kind; k != "plugin" {
		t.Fatalf("a plugin: kind %q", k)
	}

	// THE CATALOG: an offering of the page's package reads installed, with
	// the version it runs, and a newer one is an update.
	installed := app.installedReleases()
	pkgs := []plugincatalog.CatalogPackage{{Platform: "*", Arch: "*", URL: "https://x/h", SHA256: "sha256:hh", Size: 1}}
	cat := &plugincatalog.Index{Version: 1, Plugins: []plugincatalog.CatalogEntry{{ID: "org.example.hello", Version: "0.2.0", Packages: pkgs}}}
	views := catalogViewsFor(cat, installed, nil)
	if len(views) != 1 || !views[0].Installed || views[0].InstalledVersion != "0.1.0" || !views[0].UpdateAvailable {
		t.Fatalf("the catalog's row of an installed page package: %+v", views)
	}

	// WHAT IT STORED: listed as installed, and a package that is gone is not.
	stored := []dashboard.PluginStoredView{{ID: "org.example.hello"}, {ID: "org.example.gone"}}
	app.markStoredInstalled(stored, nil)
	if !stored[0].Installed || stored[1].Installed {
		t.Fatalf("stored rows: %+v", stored)
	}

	// UNINSTALL: the page's package goes by its folder, as a plugin's does;
	// once the sweep has stopped it, nothing lists it or serves its page.
	if err := app.UninstallPlugin("org.example.hello"); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	eventually(t, "the page's package is no longer installed or served", func() bool {
		_, serving := app.sections.Get("hello")
		_, listed := app.installedReleases()["org.example.hello"]
		return !serving && !listed
	})
	for _, v := range app.pluginViews(app.cfg) {
		if v.ID == "org.example.hello" {
			t.Fatalf("an uninstalled page's package keeps a row: %+v", v)
		}
	}
}
