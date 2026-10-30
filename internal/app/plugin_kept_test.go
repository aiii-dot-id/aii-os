package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/plugincatalog"
	"github.com/aiii-dot-id/aii-os/internal/pluginfacility"
)

// keptCatalogConfig is the configuration of an identity in dir that holds
// cat as its catalog, for a test that stops and starts it again.
func keptCatalogConfig(t *testing.T, dir string, cat plugincatalog.Index) *Config {
	t.Helper()
	catDir, rootPath, _ := signedCatalogDir(t, dir, cat)
	return &Config{
		Identity: IdentityConfig{KeyPath: filepath.Join(dir, "identity.sec"),
			LedgerPath: filepath.Join(dir, "ledger.jsonl"), DBPath: filepath.Join(dir, "aii.db")},
		LLM:        withTestProvider(t, dir, "test", "https://127.0.0.1:1", "m", "sk-x"),
		SourcePath: filepath.Join(dir, "config.json"),
		Dashboard:  DashboardConfig{Port: 0},
		Tools:      ToolsConfig{CWD: dir},
		Plugins:    PluginsConfig{Autoload: "T0", PlatformRoot: rootPath, CatalogDir: catDir},
		Agency:     defaultConfig().Agency,
	}
}

func bootKept(t *testing.T, cfg *Config) *App {
	t.Helper()
	app := New(cfg)
	if err := startLiveForTest(app); err != nil {
		t.Fatal(err)
	}
	return app
}

// slotNames is what a directory holds, sorted.
func slotNames(t *testing.T, slot string) []string {
	t.Helper()
	entries, err := os.ReadDir(slot)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	sort.Strings(out)
	return out
}

// keptNames is every release kept beside the slots, by file name.
func keptNames(t *testing.T, dir string) []string {
	t.Helper()
	m, err := filepath.Glob(filepath.Join(dir, "plugins", keptPrefix+"*"))
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, p := range m {
		out = append(out, filepath.Base(p))
	}
	sort.Strings(out)
	return out
}

func catalogEntryFor(t *testing.T, id, version, pkg string) plugincatalog.CatalogEntry {
	t.Helper()
	raw, err := os.ReadFile(pkg)
	if err != nil {
		t.Fatal(err)
	}
	url, sha := servePackage(t, raw)
	return plugincatalog.CatalogEntry{ID: id, Version: version, Tier: "T1",
		Packages: []plugincatalog.CatalogPackage{{Platform: "*", Arch: "*", URL: url, SHA256: sha, Size: int64(len(raw))}}}
}

// AN UPDATE KEEPS THE RELEASE IT REPLACES, AND PUTS IT BACK ONLY WHEN
// NOTHING SERVES (h-plugin-admit-04). 0.1.0 serves. The catalog's 0.2.0
// verifies, so Install places it, and its declared contract cannot be
// honoured, so the facility refuses it for good where it stands up. While
// 0.1.0 serves, its package is kept beside the slots, out of the slot the
// sweep reads, and left there: putting it back would change the slot
// under what serves. After a restart nothing serves; 0.2.0 is started
// first and refused for good again, and then 0.1.0 is back in its slot,
// serving, and the refused package and the kept file are gone.
func TestAnUpdateKeepsTheServingReleaseAndPutsItBackOnlyWhenNothingServes(t *testing.T) {
	dir := catalogInstallDir(t)
	const id = "org.example.kept"
	strict := []byte(`{"type":"object","properties":{"id":{"type":"integer","minimum":1}},"required":["id"],"additionalProperties":false}`)
	v1 := buildContractVer(t, dir, id, "0.1.0", strict)
	bad := buildContractVer(t, dir, id, "0.2.0", nil)
	v1Bytes, err := os.ReadFile(v1)
	if err != nil {
		t.Fatal(err)
	}
	installPluginDir(t, dir, id, v1)
	cfg := keptCatalogConfig(t, dir, plugincatalog.Index{Version: 1, Plugins: []plugincatalog.CatalogEntry{catalogEntryFor(t, id, "0.2.0", bad)}})
	t.Chdir(dir)
	slot := filepath.Join(dir, "plugins", id)

	app := bootKept(t, cfg)
	stopped := false
	t.Cleanup(func() {
		if !stopped {
			stopApp(t, app)
		}
	})
	awaitPluginVersion(t, app, id, "0.1.0")
	app.pkgFetch = loopbackFetcher()
	if err := app.InstallFromCatalog(context.Background(), id); err != nil {
		t.Fatalf("install: %v", err)
	}
	wantSlot := id + "-0.2.0.aiiospkg"
	wantKept := keptPrefix + id + keptSep + id + "-0.1.0.aiiospkg"
	if got := slotNames(t, slot); strings.Join(got, ",") != wantSlot {
		t.Fatalf("the update stands alone in the slot the sweep reads: slot holds %v, want %v", got, wantSlot)
	}
	if got := keptNames(t, dir); strings.Join(got, ",") != wantKept {
		t.Fatalf("the release that serves is kept beside the slots: kept %v, want %v", got, wantKept)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "plugins", wantKept)); err != nil || !bytes.Equal(b, v1Bytes) {
		t.Fatalf("the kept file is not the serving release's package (%v)", err)
	}

	// The update is refused for good beside what serves, and nothing is
	// moved all the while: every snapshot is judged as it comes, so a
	// put-back or a removal would show here first.
	unchanged := func(when string) {
		t.Helper()
		if got := slotNames(t, slot); strings.Join(got, ",") != wantSlot {
			t.Fatalf("a kept release was put back while its predecessor serves (%s): slot holds %v, want %v", when, got, wantSlot)
		}
		if got := keptNames(t, dir); strings.Join(got, ",") != wantKept {
			t.Fatalf("the kept release was removed before the update served (%s): kept %v, want %v", when, got, wantKept)
		}
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		unchanged("while the update is tried")
		var final bool
		for _, v := range app.pluginFacility().Snapshot().Instances {
			if v.ID == id && v.Refusal != nil && v.Refusal.Version == "0.2.0" && v.RefusedForGood {
				final = true
			}
		}
		if final {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for the update's refusal")
		}
		time.Sleep(10 * time.Millisecond)
	}
	app.applyFacilitySnapshot() // the judgment, on the word that it is refused
	unchanged("once it is refused for good")
	if v, _ := activeRelease(app, id); v != "0.1.0" {
		t.Fatalf("the release that serves keeps serving beside the refused update, got %q", v)
	}

	// A restart: nothing serves, and the kept release is judged there.
	stopApp(t, app)
	stopped = true
	again := bootKept(t, cfg)
	t.Cleanup(func() { stopApp(t, again) })
	deadline = time.Now().Add(30 * time.Second)
	for {
		if v, _ := activeRelease(again, id); v == "0.1.0" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("after a restart the refused update was not replaced by the release that served: slot holds %v, kept %v", slotNames(t, slot), keptNames(t, dir))
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := slotNames(t, slot); strings.Join(got, ",") != id+"-0.1.0.aiiospkg" {
		t.Fatalf("once put back the slot holds the release that served and nothing else: %v", got)
	}
	if got := keptNames(t, dir); len(got) != 0 {
		t.Fatalf("once put back nothing is kept: %v", got)
	}
}

// A KEPT RELEASE GOES ONCE THE UPDATE SERVES. 1.0.0 serves; the catalog's
// 2.0.0 is installed, 1.0.0 is kept beside the slots, and 2.0.0 is
// admitted; then the kept release is removed and the slot holds 2.0.0
// alone.
func TestAKeptReleaseIsRemovedOnceTheUpdateServes(t *testing.T) {
	dir := catalogInstallDir(t)
	const id = "org.example.keptgood"
	v1 := buildResponderVer(t, dir, id, "1.0.0", "")
	v2 := buildResponderVer(t, dir, id, "2.0.0", "")
	installPluginDir(t, dir, id, v1)
	cfg := keptCatalogConfig(t, dir, plugincatalog.Index{Version: 1, Plugins: []plugincatalog.CatalogEntry{catalogEntryFor(t, id, "2.0.0", v2)}})
	t.Chdir(dir)
	slot := filepath.Join(dir, "plugins", id)
	app := bootKept(t, cfg)
	t.Cleanup(func() { stopApp(t, app) })
	awaitPluginVersion(t, app, id, "1.0.0")
	app.pkgFetch = loopbackFetcher()
	if err := app.InstallFromCatalog(context.Background(), id); err != nil {
		t.Fatalf("install: %v", err)
	}
	awaitPluginVersion(t, app, id, "2.0.0")
	deadline := time.Now().Add(30 * time.Second)
	for {
		got, kept := slotNames(t, slot), keptNames(t, dir)
		if strings.Join(got, ",") == id+"-2.0.0.aiiospkg" && len(kept) == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the update serves and the release it replaced is still kept: slot holds %v, kept %v", got, kept)
		}
		app.applyFacilitySnapshot()
		time.Sleep(10 * time.Millisecond)
	}
	for _, v := range app.pluginFacility().Snapshot().Instances {
		if v.ID == id && (v.Package == "" || v.State != pluginfacility.StateActive) {
			t.Fatalf("the update serves from its slot: %+v", v)
		}
	}
}

// THE PLUGIN SERVES THROUGHOUT AN UPDATE, AND NO SCAN SEES ITS SLOT HALF
// CHANGED (h-plugin-admit-04). A scan that saw the update beside the
// release it replaces refused the slot whole, and one that saw the slot
// empty took the plugin for removed: either stopped the release that
// serves before the update served. 1.0.0 serves; Install sets it aside
// and places 2.0.0, and the test holds that change open for well past the
// watcher's debounce, so a scan that did not wait for it would run inside
// it. Every snapshot the facility publishes from 1.0.0 serving to 2.0.0
// serving must show a release serving, no scan may refuse the slot, and
// the log may not say it was.
func TestThePluginServesThroughoutAnUpdateAndNoScanSeesItsSlotHalfChanged(t *testing.T) {
	dir := catalogInstallDir(t)
	const id = "org.example.keptswap"
	v1 := buildResponderVer(t, dir, id, "1.0.0", "")
	v2 := buildResponderVer(t, dir, id, "2.0.0", "")
	installPluginDir(t, dir, id, v1)
	cfg := keptCatalogConfig(t, dir, plugincatalog.Index{Version: 1, Plugins: []plugincatalog.CatalogEntry{catalogEntryFor(t, id, "2.0.0", v2)}})
	t.Chdir(dir)
	slot := filepath.Join("plugins", id)
	logs := logsink.CaptureForTest(t)
	app := bootKept(t, cfg)
	t.Cleanup(func() { stopApp(t, app) })
	awaitPluginVersion(t, app, id, "1.0.0")

	// The watch: every snapshot from now until 2.0.0 serves.
	var (
		mu     sync.Mutex
		gaps   []string
		seen   int
		served = make(chan struct{})
	)
	look := func() bool {
		snap := app.pluginFacility().Snapshot()
		mu.Lock()
		defer mu.Unlock()
		seen++
		for _, sk := range snap.Skips {
			if sk.Dir == slot {
				gaps = append(gaps, fmt.Sprintf("snapshot %d: the slot was refused (%s: %s)", snap.Revision, sk.Kind, sk.Reason))
			}
		}
		for _, v := range snap.Instances {
			if v.ID != id {
				continue
			}
			var roles []string
			serving := ""
			for _, act := range v.Activations {
				roles = append(roles, fmt.Sprintf("%s %s", act.Version, act.Role))
				if act.Role == pluginfacility.RoleActive {
					serving = act.Version
				}
			}
			if serving == "2.0.0" {
				return true
			}
			if serving == "" {
				gaps = append(gaps, fmt.Sprintf("snapshot %d: nothing serves (%s; %v)", snap.Revision, v.State, roles))
			}
			return false
		}
		gaps = append(gaps, fmt.Sprintf("snapshot %d: the plugin is not there", snap.Revision))
		return false
	}
	ch, unsubscribe := app.pluginFacility().Subscribe()
	stopWatch := make(chan struct{})
	watched := make(chan struct{})
	go func() {
		defer close(watched)
		defer unsubscribe()
		if look() {
			close(served)
			return
		}
		for {
			select {
			case <-stopWatch:
				return
			case <-ch:
			}
			if look() {
				close(served)
				return
			}
		}
	}()
	t.Cleanup(func() {
		select {
		case <-watched:
		default:
			close(stopWatch)
			<-watched
		}
	})

	held := false
	app.slotChangeHeld = func() {
		held = true
		time.Sleep(time.Second)
	}
	app.pkgFetch = loopbackFetcher()
	if err := app.InstallFromCatalog(context.Background(), id); err != nil {
		t.Fatalf("install: %v", err)
	}
	if !held {
		t.Fatal("the install did not set the serving release aside, so the change was never held open")
	}
	select {
	case <-served:
	case <-time.After(30 * time.Second):
		mu.Lock()
		defer mu.Unlock()
		t.Fatalf("2.0.0 never served; %d snapshots, gaps %v", seen, gaps)
	}
	<-watched
	mu.Lock()
	defer mu.Unlock()
	if len(gaps) > 0 {
		t.Fatalf("the plugin was without a serving release across the update, before the update served: %v", gaps)
	}
	if logs.Contains("more than one package") {
		t.Fatalf("a scan saw the update beside the release it replaces and refused the slot: %s", logs.String())
	}
}

// scanAsReleased0123 is v0.1.23's scan of plugins/, the rule a rollback
// runs, transcribed from `git show v0.1.23:internal/app/plugins.go`:
// pluginDirs (lines 607-626) lists every entry of plugins/, and
// discoverPlugins (lines 451-500) passes over an entry that is not a
// directory (line 468, `if !dst.IsDir() { continue }`) and counts in each
// directory the names ending ".aiiospkg" (line 480): exactly one is a
// package found, more than one is the directory refused whole. Kept here
// as it was released, so a later change to this release's scan does not
// move what this proves about the release a rollback runs.
func scanAsReleased0123(t *testing.T) (found, ambiguous []string) {
	t.Helper()
	entries, err := os.ReadDir("plugins")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		pdir := filepath.Join("plugins", e.Name())
		dst, err := os.Stat(pdir)
		if err != nil {
			t.Fatal(err)
		}
		if !dst.IsDir() {
			continue
		}
		inner, err := os.ReadDir(pdir)
		if err != nil {
			t.Fatal(err)
		}
		var pkgs []string
		for _, f := range inner {
			if strings.HasSuffix(f.Name(), ".aiiospkg") {
				pkgs = append(pkgs, filepath.Join(pdir, f.Name()))
			}
		}
		switch {
		case len(pkgs) == 1:
			found = append(found, pkgs[0])
		case len(pkgs) > 1:
			ambiguous = append(ambiguous, pdir)
		}
	}
	return found, ambiguous
}

// A KEPT RELEASE IS NO PLUGIN TO 0.1.23 (rollback rule 1). A failing 0.2.0
// rolls back to 0.1.23 with whatever stands in plugins/: an update in its
// slot and the release it replaced kept beside the slots. 0.1.23's scan
// finds the update alone and refuses nothing; this release's scan finds
// the same. The kept file is no table, column or event type: a file in
// plugins/ that is not a directory, as 0.1.23's own downloads are.
func TestAKeptReleaseIsNoPluginToTheScanARollbackRuns(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	const id = "org.example.kept"
	slot := filepath.Join("plugins", id)
	if err := os.MkdirAll(slot, 0o750); err != nil {
		t.Fatal(err)
	}
	update := filepath.Join(slot, id+"-0.2.0.aiiospkg")
	for _, p := range []string{update, keptPath(id, id+"-0.1.0.aiiospkg")} {
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("package"), 0o640); err != nil {
			t.Fatal(err)
		}
	}
	found, ambiguous := scanAsReleased0123(t)
	if len(ambiguous) != 0 || strings.Join(found, ",") != update {
		t.Fatalf("0.1.23's scan sees the kept release: found %v, refused %v; want %s alone", found, ambiguous, update)
	}
	disc := (&App{}).discoverPlugins()
	if disc.Err != nil || len(disc.Ambiguous) != 0 || len(disc.Found) != 1 || disc.Found[0].Package != update {
		t.Fatalf("this release's scan sees the kept release: %+v; want %s alone", disc, update)
	}
}

// WHAT A KEPT RELEASE BECOMES WITH NOTHING SERVING, judged alone. A slot
// that holds no package is a put-back, or an install's setting aside,
// that a stop interrupted: the kept release goes back. A slot that is
// gone is a plugin uninstalled, by 0.1.23 after a rollback too (its
// Uninstall removes plugins/<id> and knows nothing of the kept file): the
// kept release goes, and nothing is put back. A slot whose package has no
// answer yet is left as it is.
func TestAKeptReleaseGoesBackToAnEmptySlotAndGoesWithAnUninstalledOne(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	a := &App{}
	write := func(p string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("package"), 0o640); err != nil {
			t.Fatal(err)
		}
	}
	gone := func(p string) bool {
		_, err := os.Lstat(p)
		return errors.Is(err, fs.ErrNotExist)
	}

	empty := keptPath("org.example.empty", "org.example.empty-1.aiiospkg")
	write(empty)
	if err := os.Mkdir(filepath.Join("plugins", "org.example.empty"), 0o750); err != nil {
		t.Fatal(err)
	}
	a.judgeKept(empty, pluginfacility.Snapshot{})
	if back := filepath.Join("plugins", "org.example.empty", "org.example.empty-1.aiiospkg"); gone(back) || !gone(empty) {
		t.Fatalf("an empty slot gets its kept release back: %v in the slot, kept file gone %v", !gone(back), gone(empty))
	}

	uninstalled := keptPath("org.example.gone", "org.example.gone-1.aiiospkg")
	write(uninstalled)
	a.judgeKept(uninstalled, pluginfacility.Snapshot{})
	if !gone(uninstalled) || !gone(filepath.Join("plugins", "org.example.gone")) {
		t.Fatal("a kept release whose slot is gone is removed, and no slot is made for it")
	}

	waiting := keptPath("org.example.wait", "org.example.wait-1.aiiospkg")
	write(waiting)
	write(filepath.Join("plugins", "org.example.wait", "org.example.wait-2.aiiospkg"))
	a.judgeKept(waiting, pluginfacility.Snapshot{})
	if gone(waiting) || !gone(filepath.Join("plugins", "org.example.wait", "org.example.wait-1.aiiospkg")) {
		t.Fatal("a kept release is left alone while its update has no answer")
	}
}

// AN UNINSTALL TAKES THE KEPT RELEASE WITH THE SLOT, at once and not at
// the next judgment: an install of the same id that follows starts
// afresh, and its refusal must not bring back a release the operator
// removed.
func TestAnUninstallTakesTheKeptReleaseWithTheSlot(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	const id = "org.example.uninstalled"
	slot := filepath.Join("plugins", id)
	if err := os.MkdirAll(slot, 0o750); err != nil {
		t.Fatal(err)
	}
	kept := keptPath(id, id+"-1.aiiospkg")
	for _, p := range []string{filepath.Join(slot, id+"-2.aiiospkg"), kept} {
		if err := os.WriteFile(p, []byte("package"), 0o640); err != nil {
			t.Fatal(err)
		}
	}
	if err := (&App{}).UninstallPlugin(id); err != nil {
		t.Fatal(err)
	}
	if got := keptNames(t, dir); len(got) != 0 {
		t.Fatalf("the uninstall left the release kept for the plugin: %v", got)
	}
}
