package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/hold"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/plugincatalog"
	"github.com/aiii-dot-id/aii-os/internal/pluginfacility"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
)

// heldSlot makes plugins/<id> under dir with a package file in it, marked
// or not, and returns the package's path as the scan would name it.
func heldSlot(t *testing.T, dir, id string, marked bool) string {
	t.Helper()
	slot := filepath.Join(dir, "plugins", id)
	if err := os.MkdirAll(slot, 0o750); err != nil {
		t.Fatal(err)
	}
	pkg := filepath.Join(slot, id+"-1.0.0.aiiospkg")
	if err := os.WriteFile(pkg, []byte("a package"), 0o640); err != nil {
		t.Fatal(err)
	}
	if marked {
		if _, err := writeHeldMark(slot); err != nil {
			t.Fatal(err)
		}
	}
	return pkg
}

// serves makes id answered by an activation of the given runtime, as the
// commit of a start leaves it.
func serves(a *App, id, runsAs string) {
	a.pluginMu.Lock()
	defer a.pluginMu.Unlock()
	if a.activeMeta == nil {
		a.activeMeta = map[string]activePkgMeta{}
	}
	a.activeMeta[id] = activePkgMeta{owner: &pluginhost.Activation{ID: id, Plugin: &pluginhost.ActivePlugin{ID: id, Runtime: runsAs}}}
}

// THE HOLD IS ASKED WHERE EVERY START IS ASKED, AND IT HOLDS A START AS A
// NATIVE PROGRAM ONLY. The policy's one question keeps a marked native
// package off and says what its signed package says; a module in a marked
// slot and a native package with no mark (installed before the hold
// existed, or copied in by hand) are answered as they always were. A
// module that serves exempts nothing; a native release that serves does.
func TestThePolicyHoldsAMarkedNativePackageAndNothingElse(t *testing.T) {
	dir := t.TempDir()
	const id = "org.example.engine"
	marked, unmarked := heldSlot(t, dir, id, true), heldSlot(t, dir, "org.example.before", false)
	a := &App{}
	pol := a.pluginPolicy(Config{Plugins: PluginsConfig{Autoload: "T1"}}, packagefmt.TierT1, false)
	ev := func(pkg string, native bool) pluginfacility.Evidence {
		return pluginfacility.Evidence{ID: id, Version: "1.0.0", Package: pkg, Tier: "T3", Native: native,
			Capabilities: []string{"fs.private", "net.outbound:api.example.org:443"}}
	}
	const words = "Runs as a native program; signed for fs.private, net.outbound:api.example.org:443. Not started yet: press Start when you have read this."

	if ok, why := pol.Allows(ev(marked, true)); ok || why != words {
		t.Fatalf("a marked native package is held with what its signed package says: allowed=%v %q", ok, why)
	}
	if r := a.startRefusal("T1", packagefmt.TierT1, ev(marked, true)); r == nil || r.Requirement != startNeedsOperator || r.ID != id {
		t.Fatalf("the refusal names the requirement, the operator's Start: %+v", r)
	}
	bare := ev(marked, true)
	bare.Capabilities = nil
	if _, why := pol.Allows(bare); why != fmt.Sprintf(nativeHoldWords, "nothing") {
		t.Fatalf("a release signed for no capability says so: %q", why)
	}
	if ok, why := pol.Allows(ev(marked, false)); !ok {
		t.Fatalf("a module starts whatever its slot holds: %q", why)
	}
	if ok, why := pol.Allows(ev(unmarked, true)); !ok {
		t.Fatalf("a native package with no mark, installed before the hold or copied in by hand, starts as it did: %q", why)
	}
	// The level is asked first and keeps its own words and requirement.
	none := a.startRefusal("none", 0, ev(marked, true))
	if none == nil || none.Requirement != startNeedsLevel {
		t.Fatalf("under autoload none the level is what is unmet: %+v", none)
	}

	// A MODULE THAT SERVES EXEMPTS NOTHING: the native release that would
	// replace it is this plugin's first start as a native program.
	serves(a, id, "wasm_component")
	if ok, why := pol.Allows(ev(marked, true)); ok || why != words {
		t.Fatalf("a native candidate started beside the module it replaces, with no Start: allowed=%v %q", ok, why)
	}
	// A native release that serves is never stopped by the mark: an update
	// from one native release to another, and a mark made by hand beside a
	// running native plugin, are both answered yes.
	serves(a, id, "native_t3_component")
	if ok, why := pol.Allows(ev(marked, true)); !ok {
		t.Fatalf("the mark stopped a native plugin that serves: %q", why)
	}

	// And what serves is not a start at all: the very package that
	// answers now is never stopped by a mark made beside it, whatever it
	// runs as.
	sameBytes := ev(marked, true)
	sameBytes.PackageHash, sameBytes.ManifestHash = "sha256:these", "sha256:manifest"
	a.pluginMu.Lock()
	a.activeMeta[id] = activePkgMeta{owner: &pluginhost.Activation{ID: id, Hash: "sha256:these", ManifestHash: "sha256:manifest",
		Plugin: &pluginhost.ActivePlugin{ID: id, Runtime: "wasm_component"}}}
	a.pluginMu.Unlock()
	if ok, why := pol.Allows(sameBytes); !ok {
		t.Fatalf("the mark stopped the package that serves: %q", why)
	}
	sameBytes.PackageHash = "sha256:other"
	if ok, _ := pol.Allows(sameBytes); ok {
		t.Fatal("other bytes, native, were started beside a module that serves")
	}

	// The mark itself: there, not there, and not readable.
	if !heldMark(filepath.Dir(marked)) || heldMark(filepath.Dir(unmarked)) {
		t.Fatal("the mark is read from the slot")
	}
	if !heldMark("no\x00such") {
		t.Fatal("a mark that cannot be read does not hold: the hold fails open")
	}
	// And whose it is: a second writer finds the first one's mark and
	// claims nothing, so a failed attempt can take back only its own.
	if wrote, err := writeHeldMark(filepath.Dir(unmarked)); !wrote || err != nil {
		t.Fatalf("a first mark is the writer's own: wrote=%v %v", wrote, err)
	}
	if wrote, err := writeHeldMark(filepath.Dir(unmarked)); wrote || err != nil {
		t.Fatalf("a mark already there is not the second writer's: wrote=%v %v", wrote, err)
	}
}

// INSTALL HOLDS A PLUGIN'S FIRST START AS A NATIVE PROGRAM, whatever its
// slot held before. What Install has fetched is read from the signed
// package: a native package lands with the mark already beside it unless a
// native release of that plugin serves; a WASM module lands alone, as
// before. A package that does not verify, or verifies as another plugin
// than the catalog entry it was fetched for, is refused before any slot
// exists for it.
func TestInstallHoldsANativeProgramAndLeavesAModuleAsItWas(t *testing.T) {
	dir := t.TempDir()
	roots, nativePkg := componentSetsPackage(t, "1.0.0", fullSet, smallSet)
	nativeRaw, err := os.ReadFile(nativePkg)
	if err != nil {
		t.Fatal(err)
	}
	// The fixture is what the test says it is: a package that verifies
	// under these roots and whose signed manifest runs natively here.
	res, err := pluginhost.Verify(nativePkg, &pluginhost.Options{Roots: roots})
	if err != nil {
		t.Fatalf("the native fixture does not verify: %v", err)
	}
	if native, _ := pluginhost.NativeHere(res); !native {
		t.Fatal("the native fixture's signed manifest does not read as native on this host")
	}
	const moduleID, otherID, junkID = "org.example.module", "org.example.other", "org.example.junk"
	moduleRaw, err := os.ReadFile(buildResponderVer(t, dir, moduleID, "1.0.0", ""))
	if err != nil {
		t.Fatal(err)
	}
	junkRaw := []byte("bytes that are no package")
	entry := func(id, tier, platform, arch string, raw []byte) plugincatalog.CatalogEntry {
		url, sha := servePackage(t, raw)
		return plugincatalog.CatalogEntry{ID: id, Version: "1.0.0", Tier: tier, Packages: []plugincatalog.CatalogPackage{
			{Platform: platform, Arch: arch, URL: url, SHA256: sha, Size: int64(len(raw))}}}
	}
	here, arch := packagefmt.HostPlatform(), packagefmt.HostArch()
	catDir, _, root := signedCatalogDir(t, dir, plugincatalog.Index{Version: 1, Plugins: []plugincatalog.CatalogEntry{
		entry(componentSetsID, "T3", here, arch, nativeRaw),
		entry(moduleID, "T0", "*", "*", moduleRaw),
		// An entry under one name whose package is signed as another plugin.
		entry(otherID, "T3", here, arch, nativeRaw),
		entry(junkID, "T1", "*", "*", junkRaw),
	}})
	slotOf := func(id string) string {
		t.Helper()
		entries, err := os.ReadDir(filepath.Join("plugins", id))
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		return strings.Join(names, ",")
	}
	// fresh is an App in a new, empty install directory.
	fresh := func(t *testing.T) *App {
		t.Helper()
		t.Chdir(t.TempDir())
		a := &App{}
		a.pluginOpts = &pluginhost.Options{Roots: roots}
		a.catalog.Open(catDir, "", root, nil)
		a.pkgFetch = loopbackFetcher()
		return a
	}
	// installedBefore puts an earlier release's package in the slot.
	installedBefore := func(t *testing.T) {
		t.Helper()
		slot := filepath.Join("plugins", componentSetsID)
		if err := os.MkdirAll(slot, 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(slot, componentSetsID+"-0.9.0.aiiospkg"), []byte("the release installed before"), 0o640); err != nil {
			t.Fatal(err)
		}
	}
	install := func(t *testing.T, a *App, id string) {
		t.Helper()
		if err := a.InstallFromCatalog(context.Background(), id); err != nil {
			t.Fatalf("%s: %v", id, err)
		}
	}
	nativeFile := componentSetsID + "-1.0.0.aiiospkg"
	heldNative := nativeFile + "," + heldFile // the slot's entries, in name order

	t.Run("a first install", func(t *testing.T) {
		a := fresh(t)
		install(t, a, componentSetsID)
		install(t, a, moduleID)
		if got := slotOf(componentSetsID); got != heldNative {
			t.Fatalf("a native program is placed with its mark beside it, got %s", got)
		}
		if got := slotOf(moduleID); got != moduleID+"-1.0.0.aiiospkg" {
			t.Fatalf("a module is placed alone, as it always was, got %s", got)
		}
	})
	t.Run("a module that serves is updated to a native release", func(t *testing.T) {
		a := fresh(t)
		installedBefore(t)
		serves(a, componentSetsID, "wasm_component")
		install(t, a, componentSetsID)
		if got := slotOf(componentSetsID); got != heldNative {
			t.Fatalf("the plugin's first native release is held though a module of it serves, got %s", got)
		}
	})
	t.Run("a slot whose package never ran takes a native release", func(t *testing.T) {
		a := fresh(t)
		installedBefore(t) // kept off by the level, unverified or a duplicate: nothing serves
		install(t, a, componentSetsID)
		if got := slotOf(componentSetsID); got != heldNative {
			t.Fatalf("a native release over a package that never ran is held, got %s", got)
		}
	})
	t.Run("a native release that serves is updated to another", func(t *testing.T) {
		a := fresh(t)
		installedBefore(t)
		serves(a, componentSetsID, "native_t3_component")
		install(t, a, componentSetsID)
		if got := slotOf(componentSetsID); got != nativeFile {
			t.Fatalf("an update from a native release that serves is not held: the slot holds the new release and no mark, got %s", got)
		}
	})
	t.Run("an update of a release still held stays held", func(t *testing.T) {
		a := fresh(t)
		install(t, a, componentSetsID)
		install(t, a, componentSetsID)
		if got := slotOf(componentSetsID); got != heldNative {
			t.Fatalf("installing again over a held release lost or doubled its mark, got %s", got)
		}
	})
	t.Run("two installs of one plugin at once", func(t *testing.T) {
		a := fresh(t)
		var wg sync.WaitGroup
		errs := make([]error, 4)
		for i := range errs {
			wg.Add(1)
			go func() { defer wg.Done(); errs[i] = a.InstallFromCatalog(context.Background(), componentSetsID) }()
		}
		wg.Wait()
		for _, err := range errs {
			if err != nil {
				t.Fatalf("an install beside another of the same plugin failed: %v", err)
			}
		}
		if got := slotOf(componentSetsID); got != heldNative {
			t.Fatalf("installs at once left the slot as %s", got)
		}
	})
	t.Run("a mark that cannot be written", func(t *testing.T) {
		if runtime.GOOS == "windows" || os.Geteuid() == 0 {
			t.Skip("a directory's mode does not stop this account from writing in it")
		}
		a := fresh(t)
		slot := filepath.Join("plugins", componentSetsID)
		if err := os.MkdirAll(slot, 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(slot, 0o500); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(slot, 0o750) })
		err := a.InstallFromCatalog(context.Background(), componentSetsID)
		var r *CatalogInstallRefusal
		if !errors.As(err, &r) || r.Reason != "place" {
			t.Fatalf("the install is refused at the step that failed, got %v", err)
		}
		if got := slotOf(componentSetsID); got != "" {
			t.Fatalf("a native program was placed where it could not be held: %s", got)
		}
		if m, _ := filepath.Glob(filepath.Join("plugins", ".download-*")); len(m) != 0 {
			t.Fatalf("the refused install left its download behind: %v", m)
		}
	})
	t.Run("a package signed as another plugin", func(t *testing.T) {
		a := fresh(t)
		err := a.InstallFromCatalog(context.Background(), otherID)
		var r *CatalogInstallRefusal
		if !errors.As(err, &r) || r.Reason != "identity" || r.ID != otherID || r.Cleanup != nil {
			t.Fatalf("an entry whose package verifies as another plugin is refused as such, got %v", err)
		}
		assertNoInstallResidue(t, ".", otherID)
		assertNoInstallResidue(t, ".", componentSetsID)
	})
	t.Run("a package that does not verify", func(t *testing.T) {
		a := fresh(t)
		err := a.InstallFromCatalog(context.Background(), junkID)
		var r *CatalogInstallRefusal
		if !errors.As(err, &r) || r.Reason != "unverified" || r.Cleanup != nil {
			t.Fatalf("a package the host cannot verify is refused as such, got %v", err)
		}
		assertNoInstallResidue(t, ".", junkID)
	})
}

// nativeByDir is the facility's runtime for a slot of made-up bytes: every
// package verifies as the plugin its directory names and, unless its file
// is the one named module, as a native program signed for one capability;
// starting it is counted and adopted into the application's view, as the
// real commit does.
type nativeByDir struct {
	*auditDiscoveryRuntime
	module string
}

func (r nativeByDir) Verify(_ context.Context, pkg string) (pluginfacility.Evidence, error) {
	return pluginfacility.Evidence{ID: filepath.Base(filepath.Dir(pkg)), Version: "1.0.0", Package: pkg, PackageHash: "hash:" + pkg,
		Tier: "T3", Native: filepath.Base(pkg) != r.module, Capabilities: []string{"fs.private"}}, nil
}

// heldRig is an App over a real scan of plugins/ in dir and a facility
// driven by rt, with nothing else running: the test asks for each scan.
func heldRig(t *testing.T, dir, module string) (a *App, f *pluginfacility.Facility, rt *auditDiscoveryRuntime, scan func()) {
	t.Helper()
	t.Chdir(dir)
	a = &App{cfg: &Config{Plugins: PluginsConfig{Autoload: "T1"}}, sweepPoke: make(chan struct{}, 1)}
	rt = &auditDiscoveryRuntime{a: a}
	f = pluginfacility.New(pluginfacility.Config{Runtime: nativeByDir{rt, module}, Discover: a.discoverPlugins})
	a.facilityOnce.Do(func() { a.facility = f })
	ctx, cancel := context.WithCancel(context.Background())
	f.Attach(ctx)
	t.Cleanup(func() { cancel(); f.Close() })
	return a, f, rt, func() { f.Rescan(a.pluginPolicy(*a.cfg, packagefmt.TierT1, false)) }
}

// AN UPDATE THAT TURNS A MODULE INTO A NATIVE PROGRAM WAITS FOR START. The
// module serves; Install places the native release with its mark (as the
// install test shows it does) in place of the module's package. The scan
// does not start the native release beside the module: the module, whose
// package is gone, stops, and the plugin says not started until Start.
func TestAnUpdateOfAModuleToANativeReleaseWaitsForStart(t *testing.T) {
	dir := t.TempDir()
	const id = "org.example.engine"
	module := heldSlot(t, dir, id, false)
	a, _, rt, scan := heldRig(t, dir, filepath.Base(module))
	scan()
	auditDiscoveryWait(t, "the module to serve", func() bool { return rt.starts.Load() == 1 && a.activePlugin(id) != nil })

	// What Install leaves of the update: the mark, then the native
	// release, and the module's package gone.
	slot := filepath.Join("plugins", id)
	if _, err := writeHeldMark(slot); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(slot, id+"-2.0.0.aiiospkg"), []byte("the native release"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(slot, filepath.Base(module))); err != nil {
		t.Fatal(err)
	}
	scan()
	auditDiscoveryWait(t, "the module, whose package was replaced, to stop", func() bool { return a.activePlugin(id) == nil })
	if rt.starts.Load() != 1 {
		t.Fatalf("the native release started beside the module with no Start: %d starts", rt.starts.Load())
	}
	held := false
	for _, v := range a.pluginPendingViews() {
		held = held || (v.ID == id && v.Phase == lifeUnstarted)
	}
	if !held {
		t.Fatalf("the plugin does not say not started: %+v", a.pluginPendingViews())
	}
	if err := a.StartPlugin(id); err != nil {
		t.Fatal(err)
	}
	scan()
	auditDiscoveryWait(t, "the native release to start at Start", func() bool { return rt.starts.Load() == 2 && a.activePlugin(id) != nil })
}

func TestAHeldPluginsRowFromInstallToStarted(t *testing.T) {
	dir := t.TempDir()
	const id = "org.example.engine"
	heldSlot(t, dir, id, true)
	a, f, rt, scan := heldRig(t, dir, "")
	card := func() *dashboard.PluginPendingView {
		for _, v := range a.pluginPendingViews() {
			if v.ID == id {
				return &v
			}
		}
		return nil
	}
	catalogRow := func() dashboard.CatalogEntryView {
		t.Helper()
		rows := catalogViewsFor(&plugincatalog.Index{Version: 1, Plugins: []plugincatalog.CatalogEntry{{ID: id, Version: "1.0.0", Tier: "T3",
			Packages: []plugincatalog.CatalogPackage{{Platform: "*", Arch: "*", URL: "https://h/p.aiiospkg", SHA256: "sha256:aa", Size: 1}}}}}, nil, a.pendingSummaries())
		if len(rows) != 1 {
			t.Fatalf("one entry, one row: %+v", rows)
		}
		return rows[0]
	}
	words := fmt.Sprintf(nativeHoldWords, "fs.private")

	scan()
	if len(f.Skips()) != 1 || rt.starts.Load() != 0 {
		t.Fatalf("the held package was not kept off by the scan: skips %+v, starts %d", f.Skips(), rt.starts.Load())
	}
	if c := card(); c == nil || c.Phase != lifeUnstarted || c.Summary != words || c.Version != "1.0.0" {
		t.Fatalf("the held plugin's card is typed not started and carries the host's sentence: %+v", c)
	}
	if r := catalogRow(); r.Pending != lifeUnstarted || r.PendingText != words {
		t.Fatalf("the catalog row says not started with the same sentence, in place of Install: %+v", r)
	}
	if skips := a.pluginSkipViews(); len(skips) != 0 {
		t.Fatalf("a held plugin is drawn a second time among what policy keeps off: %+v", skips)
	}
	var refusal *pluginStartRefusal
	if err := a.RetryPlugin(id); !errors.As(err, &refusal) || refusal.Requirement != startNeedsOperator {
		t.Fatalf("Try again on a held plugin is answered with what it waits on: %v", err)
	}
	scan() // another pass: a reload, the directory watch, a retry
	if rt.starts.Load() != 0 {
		t.Fatal("another pass started a held plugin")
	}

	// Start is taken; no scan has run yet. The row says starting.
	if err := a.StartPlugin(id); err != nil {
		t.Fatal(err)
	}
	if c := card(); c == nil || c.Phase != lifeStarting {
		t.Fatalf("between Start and the scan the card does not say starting: %+v", c)
	}
	if r := catalogRow(); r.Pending != lifeStarting {
		t.Fatalf("between Start and the scan the catalog row offers Install again: %+v", r)
	}
	if skips := a.pluginSkipViews(); len(skips) != 0 {
		t.Fatalf("a plugin that is starting is listed as kept off: %+v", skips)
	}
	if err := a.RetryPlugin(id); errors.As(err, &refusal) {
		t.Fatalf("once Start is taken Try again is no longer answered with the hold: %v", err)
	}
	scan()
	auditDiscoveryWait(t, "the plugin to start once the scan has run", func() bool { return rt.starts.Load() == 1 && a.activePlugin(id) != nil })
	if len(f.Skips()) != 0 {
		t.Fatalf("a started plugin is still listed as kept off: %+v", f.Skips())
	}
}

// IN SAFE A MARKED SLOT NOTHING HAS SCANNED IS NOT OFFERED AS AN INSTALL.
// A boot that stopped in SAFE scans nothing and cannot verify a package,
// so it cannot show what a held plugin is signed for and offers no Start:
// the row says SAFE holds it. Outside SAFE the same row is the scan's.
func TestInSafeAHeldPluginsRowSaysSafeHoldsIt(t *testing.T) {
	dir := t.TempDir()
	const held, module = "org.example.engine", "org.example.module"
	portable := []plugincatalog.CatalogPackage{{Platform: "*", Arch: "*", URL: "u", SHA256: "s", Size: 1}}
	catDir, _, root := signedCatalogDir(t, dir, plugincatalog.Index{Plugins: []plugincatalog.CatalogEntry{
		{ID: held, Version: "1.0.0", Tier: "T3", Summary: "held", Packages: portable},
		{ID: module, Version: "1.0.0", Tier: "T1", Summary: "runs", Packages: portable},
	}})
	heldSlot(t, dir, held, true)
	heldSlot(t, dir, module, true) // a mark beside a module that runs changes nothing of its row
	t.Chdir(dir)
	a := &App{}
	a.catalog.Open(catDir, "", root, nil)
	a.pluginMu.Lock()
	a.plugins = []*pluginhost.ActivePlugin{{ID: module, Version: "1.0.0"}}
	a.pluginMu.Unlock()
	rows := func() map[string]dashboard.CatalogEntryView {
		out := map[string]dashboard.CatalogEntryView{}
		for _, v := range a.catalogViews() {
			out[v.ID] = v
		}
		return out
	}
	if r := rows()[held]; r.Pending != "" {
		t.Fatalf("outside SAFE the row is the scan's alone: %+v", r)
	}
	a.mode.mode, a.mode.reason = ModeSafe, "test: the record is frozen"
	got := rows()
	if r := got[held]; r.Pending != lifeHeld || !strings.Contains(r.PendingText, "SAFE") || r.Installed {
		t.Fatalf("in SAFE a held plugin's row is offered as an Install: %+v", r)
	}
	if r := got[module]; r.Pending != "" || !r.Installed {
		t.Fatalf("a plugin that runs is not held by SAFE's row: %+v", r)
	}
}

// nativeByWord is the application's own runtime with one word changed:
// that a package runs as a native program on this host. Everything else is
// real, so a package it calls native is verified, started, registered and
// stopped exactly as the application does those.
type nativeByWord struct{ pluginRuntime }

func (r nativeByWord) Verify(ctx context.Context, pkg string) (pluginfacility.Evidence, error) {
	ev, err := r.pluginRuntime.Verify(ctx, pkg)
	ev.Native = true
	return ev, err
}

// A HELD NATIVE PLUGIN STARTS ONLY AT THE OPERATOR'S START, in the running
// application: a restart finds it still held at boot, the identity's tool
// list has nothing of it, another pass leaves it not started, and the Start
// action on the dashboard's door starts it. A native plugin installed
// before the hold existed has no mark and starts at boot as it always did.
func TestAHeldNativePluginStartsOnlyAtTheOperatorsStart(t *testing.T) {
	dir := catalogInstallDir(t)
	const held, before = "org.example.engine", "org.example.before"
	installPluginDir(t, dir, held, buildResponderVer(t, dir, held, "1.0.0", ""))
	installPluginDir(t, dir, before, buildResponderVer(t, dir, before, "1.0.0", ""))
	t.Chdir(dir)
	mark := filepath.Join("plugins", held, heldFile)
	if _, err := writeHeldMark(filepath.Dir(mark)); err != nil {
		t.Fatal(err)
	}
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
	f := pluginfacility.New(pluginfacility.Config{Runtime: nativeByWord{newPluginRuntime(app)},
		Capacity: hostCapacity{}, Discover: app.discoverPlugins, Spawn: app.runBackground})
	app.facilityOnce.Do(func() { app.facility = f })
	if err := startLiveForTest(app); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stopApp(t, app) })
	offered := func(id string) bool {
		_, ok := app.toolReg.Get("pl_" + strings.ReplaceAll(id, ".", "_") + "_ping")
		return ok
	}

	// At boot: what was installed before the hold starts; what is held does not.
	waitUntil(t, func() bool { return offered(before) }, "the plugin with no mark to start at boot")
	waitUntil(t, func() bool { return len(app.pluginFacility().Skips()) == 1 }, "the held plugin to be kept off")
	notStarted := func(when string) {
		t.Helper()
		for _, name := range app.toolReg.Names() {
			if strings.Contains(name, "org_example_engine") {
				t.Fatalf("%s: the identity is offered %s of a plugin that has not started", when, name)
			}
		}
		if app.activePlugin(held) != nil {
			t.Fatalf("%s: the held plugin is active", when)
		}
	}
	notStarted("at boot")

	// Its card says what the signed package says: the responder is signed
	// for no capability.
	words := fmt.Sprintf(nativeHoldWords, "nothing")
	var card *dashboard.PluginPendingView
	for _, v := range app.pluginPendingViews() {
		if v.ID == held {
			card = &v
		}
	}
	if card == nil || card.Phase != lifeUnstarted || card.Summary != words || card.Version != "1.0.0" {
		t.Fatalf("the held plugin's card is typed not started and carries the host's sentence: %+v", card)
	}

	// Another pass asks the same question.
	app.pluginFacility().Rescan(app.pluginPolicy(app.configSnapshot(), packagefmt.TierT0, false))
	notStarted("after another pass")

	// The operator's Start, through the dashboard's own door.
	act := app.buildLiveHandler().PluginAct
	if err := act(dashboard.PluginAction{Action: "start", ID: held}); err != nil {
		t.Fatalf("start: %v", err)
	}
	waitUntil(t, func() bool { return offered(held) }, "the plugin to start once the operator pressed Start")
	if _, err := os.Lstat(mark); !os.IsNotExist(err) {
		t.Fatalf("Start takes the mark away: %v", err)
	}
	if err := act(dashboard.PluginAction{Action: "start", ID: held}); err != nil {
		t.Fatalf("Start pressed a second time changes nothing and is not an error: %v", err)
	}
	if err := act(dashboard.PluginAction{Action: "start", ID: "org.example.absent"}); err == nil {
		t.Fatal("Start on a plugin that is not installed was accepted")
	}
}

// A MARK THAT CANNOT BE REMOVED IS SAID, AND THE PLUGIN STAYS HELD.
func TestAStartThatCannotRemoveTheMarkSaysSoAndStaysHeld(t *testing.T) {
	t.Chdir(t.TempDir())
	const id = "org.example.engine"
	// A mark that is a directory with something in it cannot be removed.
	stuck := filepath.Join("plugins", id, heldFile, "in the way")
	if err := os.MkdirAll(stuck, 0o750); err != nil {
		t.Fatal(err)
	}
	a := &App{sweepPoke: make(chan struct{}, 1)}
	if err := a.StartPlugin(id); err == nil {
		t.Fatal("a Start that left the mark in place reported success")
	}
	if !heldMark(filepath.Join("plugins", id)) {
		t.Fatal("the plugin is no longer held")
	}
	select {
	case <-a.sweepPoke:
		t.Fatal("the sweep was asked to start a plugin whose mark is still there")
	default:
	}
	for _, bad := range []string{"", "../x", "a/b"} {
		if err := a.StartPlugin(bad); err == nil {
			t.Errorf("%q was accepted as a plugin id", bad)
		}
	}
}

func TestStartIsRefusedInEverySafePostureAndTheMarkStays(t *testing.T) {
	for _, tc := range []struct {
		name    string
		trigger safeTrigger
		says    func(reason string) string
	}{
		{"a boot that stopped in SAFE", safeRecord, safeHoldSentence},
		{"SAFE entered while running", safeRecordAtRuntime, safeHoldSentence},
		{"a start that waits for Ring 5", safeRing5Unreached, func(reason string) string {
			return "nothing new is activated " + hold.While + " (" + reason + ")"
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			const id = "org.example.engine"
			heldSlot(t, ".", id, true)
			a := &App{sweepPoke: make(chan struct{}, 1)}
			reason := safeReason(tc.trigger, "test: what held it")
			a.mode.mode, a.mode.reason, a.mode.trigger = ModeSafe, reason, tc.trigger
			err := a.StartPlugin(id)
			if err == nil || !strings.Contains(err.Error(), tc.says(reason)) {
				t.Fatalf("Start was not refused in the posture's own words: %v", err)
			}
			if tc.trigger == safeRing5Unreached && strings.Contains(err.Error(), "SAFE") {
				t.Fatalf("a start that waits is refused as SAFE: %v", err)
			}
			if !heldMark(filepath.Join("plugins", id)) {
				t.Fatal("THE MARK WAS REMOVED IN SAFE: the next live start runs a package whose words were never shown")
			}
			select {
			case <-a.sweepPoke:
				t.Fatal("the sweep was asked to start a plugin in SAFE")
			default:
			}
		})
	}
}

func TestTheDoorInstallsANativeProgramHeldAndStartReleasesIt(t *testing.T) {
	dir := t.TempDir()
	roots, nativePkg := componentSetsPackage(t, "1.0.0", fullSet, smallSet)
	raw, err := os.ReadFile(nativePkg)
	if err != nil {
		t.Fatal(err)
	}
	url, sha := servePackage(t, raw)
	catDir, _, root := signedCatalogDir(t, dir, plugincatalog.Index{Version: 1, Plugins: []plugincatalog.CatalogEntry{{ID: componentSetsID, Version: "1.0.0", Tier: "T3",
		Packages: []plugincatalog.CatalogPackage{{Platform: packagefmt.HostPlatform(), Arch: packagefmt.HostArch(), URL: url, SHA256: sha, Size: int64(len(raw))}}}}})
	t.Chdir(t.TempDir())
	a := &App{sweepPoke: make(chan struct{}, 1)}
	a.pluginOpts = &pluginhost.Options{Roots: roots}
	a.catalog.Open(catDir, "", root, nil)
	a.pkgFetch = loopbackFetcher()
	door := &dashboard.WSHandler{}
	a.wirePluginHooks(door)

	if err := door.PluginAct(dashboard.PluginAction{Action: "install", ID: componentSetsID}); err != nil {
		t.Fatalf("Install of a native program did not place it: %v", err)
	}
	slot := filepath.Join("plugins", componentSetsID)
	if _, err := os.Stat(filepath.Join(slot, componentSetsID+"-1.0.0.aiiospkg")); err != nil {
		t.Fatalf("Install placed no package: %v", err)
	}
	mark := filepath.Join(slot, "held")
	if _, err := os.Lstat(mark); err != nil {
		t.Fatalf("the native program was placed without the mark that holds it until Start: %v", err)
	}
	if err := door.PluginAct(dashboard.PluginAction{Action: "start", ID: componentSetsID}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := os.Lstat(mark); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Start left the mark in place: %v", err)
	}
}
