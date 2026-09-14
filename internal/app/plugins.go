package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/broker"
	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/fsdir"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
	"github.com/aiii-dot-id/aii-os/internal/sections"
	"github.com/aiii-dot-id/aii-os/internal/store"
	"github.com/aiii-dot-id/aii-os/internal/tools"
)

// .
// .
// .
const WorkerSubcommand = "plugin-worker"

// .
// .
// .
// .
// .
var harnessLane func() (string, []string, error)

// .
// .
// .
// .
// .
// .
// .
// .
// .
func (a *App) buildPluginOptions(st *store.Store, toolReg *tools.Registry, door *ledgerAdapter) (*pluginhost.Options, error) {
	cfg := a.configSnapshot()
	opts := &pluginhost.Options{}
	var err error
	if opts.Roots.PublisherCertifier, err = packagefmt.PinnedOrShipped(cfg.Plugins.CertifierRoot, packagefmt.KeyTypePublisherCertifier); err != nil {
		return nil, fmt.Errorf("plugins.certifier_root: %w", err)
	}
	if opts.Roots.Reviewer, err = packagefmt.PinnedOrShipped(cfg.Plugins.ReviewerRoot, packagefmt.KeyTypeReviewer); err != nil {
		return nil, fmt.Errorf("plugins.reviewer_root: %w", err)
	}
	if opts.Roots.PlatformRelease, err = packagefmt.PinnedOrShipped(cfg.Plugins.PlatformRoot, packagefmt.KeyTypePlatformRelease); err != nil {
		return nil, fmt.Errorf("plugins.platform_root: %w", err)
	}

	// .
	// .
	// .
	// .
	trustDir := filepath.Join(filepath.Dir(cfg.Identity.LedgerPath), "trust")
	a.trustDir, a.trustGuard = trustDir, trustEpochGuard{door: door, st: st}
	a.trustFinger = trustFingerprint(trustDir)
	opts.Roots.Revocation = packagefmt.LoadRevocationStatus(trustDir, opts.Roots, a.trustGuard)
	for _, line := range opts.Roots.Revocation.Describe() {
		log.Printf("plugins: %s", line)
	}
	a.catalogDir = cfg.Plugins.CatalogDir
	a.catalogCache = filepath.Join(filepath.Dir(cfg.Identity.LedgerPath), "plugins-catalog")
	a.catalogRoot = opts.Roots.PlatformRelease
	a.catalogPoke = make(chan struct{}, 1)
	a.loadCatalog(opts.Roots.PlatformRelease)

	// .
	// .
	// .
	// .
	if opts.Facilities, err = a.hostFacilities(); err != nil {
		return nil, fmt.Errorf("host facilities: %w", err)
	}
	if opts.WorkerBinary, opts.WorkerArgs, err = a.resolveWorkerBinary(); err != nil {
		return nil, err
	}
	// .
	// .
	if opts.WorkerBinary == "" {
		log.Printf("plugins: lane in-process (no supervised worker on this platform)")
	} else {
		log.Printf("plugins: lane supervised: %s", strings.Join(append([]string{opts.WorkerBinary}, opts.WorkerArgs...), " "))
	}
	// .
	opts.Settings = a.pluginSettingsSource
	opts.Acts = actProposer{a}
	if len(cfg.Plugins.Resources) > 0 {
		opts.MemoryMax = make(map[string]uint64, len(cfg.Plugins.Resources))
		opts.StreamMax = make(map[string]int, len(cfg.Plugins.Resources))
		opts.FilesMax = make(map[string]int, len(cfg.Plugins.Resources))
		for id, r := range cfg.Plugins.Resources {
			opts.MemoryMax[id] = r.MemoryMaxBytes
			opts.StreamMax[id] = r.StreamMaxBytes
			opts.FilesMax[id] = r.FilesMaxBytes
		}
	}
	// .
	// .
	dataDir := pluginDataRoot(cfg.Identity.LedgerPath)
	opts.PluginDataDir = filepath.Join(dataDir, "plugins-data")
	// .
	// .
	opts.PluginModelsDir = filepath.Join(dataDir, "plugins-models")
	opts.ModelFetcher = fetchModel
	// .
	// .
	// .
	// .
	opts.PluginRuntimeDir = filepath.Join(dataDir, "plugins-runtime")
	opts.RuntimeFetcher = fetchModel
	opts.RuntimeLimits = cfg.Plugins.Runtime.TreeLimits()
	opts.RuntimeRootsKept = cfg.Plugins.Runtime.RootsKept
	if a.runtimeRoots == nil {
		a.runtimeRoots = pluginhost.NewRuntimeRoots()
	}
	opts.RuntimeRoots = a.runtimeRoots
	protected := []string{cfg.Identity.LedgerPath, cfg.Identity.KeyPath, cfg.Identity.DBPath, cfg.SourcePath,
		filepath.Join(dataDir, "trust"), filepath.Join(dataDir, "tls"), opts.PluginDataDir, opts.PluginModelsDir, opts.PluginRuntimeDir, "plugins"}
	if abs, err := filepath.Abs("plugins"); err == nil {
		protected = append(protected, abs)
	}
	for _, prof := range cfg.Plugins.AuthProfiles {
		for _, f := range []string{prof.SecretFile, prof.ClientSecretFile, prof.TokenFile} {
			if f != "" {
				protected = append(protected, f)
			}
		}
	}

	{
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		opts.Broker, err = broker.New(broker.Config{
			Store:        st,
			Grants:       cfg.Plugins.Grants,
			AuthProfiles: cfg.Plugins.AuthProfiles,
			ObserveFetch: toolReg.NotifyFetch,
			// .
			// .
			Voice: voiceObserver{a},
			// .
			// .
			Embed: providerEmbedder{a},
			// .
			// .
			// .
			// .
			InSAFE:         func() bool { return a.currentMode() == ModeSafe },
			ProtectedPaths: protected,
			// .
			// .
			OwnListener: a.ownListener,
		})
		if err != nil {
			return nil, err
		}
	}
	return opts, nil
}

// .
func (a *App) pokePluginSweep() {
	select {
	case a.sweepPoke <- struct{}{}:
	default:
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
// .
// .
// .
// .
// .
// .
func (a *App) startPluginSweep(ctx context.Context) {
	a.sweepPoke = make(chan struct{}, 1)
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	if err := os.MkdirAll("plugins", 0o750); err != nil {
		log.Printf("plugins: cannot create plugins/ (%v) — drop-ins wait for the heartbeat", err)
	}
	w := fsdir.New(ctx, a.gate, "plugins", fsdir.Options{Depth: 1})
	// .
	// .
	// .
	// .
	// .
	var trustC <-chan struct{}
	if a.trustDir != "" {
		trustC = fsdir.New(ctx, a.gate, a.trustDir, fsdir.Options{}).C
	}
	a.runBackground(func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-w.C:
			case <-trustC:
			case <-a.sweepPoke:
			}
			if _, safe := a.SafeMode(); safe {
				continue
			}
			a.convergePlugins(ctx)
		}
	})
}

// .
// .
// .
// .
// .
func (a *App) convergePlugins(ctx context.Context) {
	cfg := a.configSnapshot()
	autoload := cfg.Plugins.Autoload
	minTier, loadNone, tierOK := autoloadTier(autoload)
	if !tierOK {
		log.Printf("plugins: autoload %q is not a level (none, T0..T3) — using T1", autoload)
		minTier, loadNone = packagefmt.TierT1, false
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
	trustChanged := false
	if a.pluginOpts != nil && a.trustDir != "" {
		if tf := trustFingerprint(a.trustDir); tf != a.trustFinger {
			a.trustFinger = tf
			a.pluginOpts.Roots.Revocation = packagefmt.LoadRevocationStatus(a.trustDir, a.pluginOpts.Roots, a.trustGuard)
			for _, line := range a.pluginOpts.Roots.Revocation.Describe() {
				log.Printf("plugins: trust directory changed — %s", line)
			}
			a.pluginVerify = nil
			trustChanged = true
		}
	}

	type found struct {
		dir, pkg    string
		size, mtime int64
	}
	var scan []found
	dirs, _ := filepath.Glob(filepath.Join("plugins", "*"))
	sort.Strings(dirs)
	var finger strings.Builder
	finger.WriteString(autoload)
	for _, pdir := range dirs {
		if st, err := os.Stat(pdir); err != nil || !st.IsDir() {
			continue
		}
		pkgs, _ := filepath.Glob(filepath.Join(pdir, "*.aiiospkg"))
		if len(pkgs) == 0 {
			continue
		}
		if len(pkgs) > 1 {
			fmt.Fprintf(&finger, "|%s!ambiguous", pdir)
			continue
		}
		st, err := os.Stat(pkgs[0])
		if err != nil {
			continue
		}
		f := found{dir: pdir, pkg: pkgs[0], size: st.Size(), mtime: st.ModTime().UnixNano()}
		scan = append(scan, f)
		fmt.Fprintf(&finger, "|%s=%s:%d:%d", pdir, f.pkg, f.size, f.mtime)
	}
	fp := finger.String()
	a.pluginMu.Lock()
	unchanged := fp == a.pluginFinger && !trustChanged
	a.pluginFinger = fp
	a.pluginMu.Unlock()
	if unchanged {
		return
	}

	// .
	for _, pdir := range dirs {
		if pkgs, _ := filepath.Glob(filepath.Join(pdir, "*.aiiospkg")); len(pkgs) > 1 {
			log.Printf("plugin dir %s: %d packages — ambiguous, REFUSED (one package per directory)", pdir, len(pkgs))
		}
	}
	secRoots := packagefmt.TrustRoots{}
	if a.pluginOpts != nil {
		secRoots = a.pluginOpts.Roots
	}
	if a.pluginVerify == nil {
		a.pluginVerify = make(map[string]verifyMemo)
	}
	type want struct {
		found
		res *packagefmt.Result
	}
	desired := map[string]want{}
	var skips []pluginSkip
	for _, f := range scan {
		memo, ok := a.pluginVerify[f.pkg]
		if !ok || memo.size != f.size || memo.mtime != f.mtime {
			res, err := packagefmt.VerifyFile(f.pkg, secRoots)
			memo = verifyMemo{size: f.size, mtime: f.mtime, res: res, err: err}
			a.pluginVerify[f.pkg] = memo
		}
		if memo.err != nil {
			log.Printf("plugin %s: verification FAILED, package skipped (identity unaffected; this is not T0 — invalid evidence refuses at every autoload level): %v", f.pkg, memo.err)
			continue
		}
		vid := memo.res.Manifest.ID
		if prev, dup := desired[vid]; dup {
			log.Printf("plugin dir %s: verified id %q already provided by %s — duplicate REFUSED", f.dir, vid, prev.dir)
			continue
		}
		if base := filepath.Base(f.dir); base != vid {
			log.Printf("plugin dir %s: note — directory name differs from verified id %q (identity comes from the signature)", f.dir, vid)
		}
		if loadNone || memo.res.Tier < minTier {
			skips = append(skips, pluginSkip{Dir: f.dir, ID: vid, Tier: memo.res.Tier.String(), Reason: fmt.Sprintf("verified %s is below plugins.autoload %s", memo.res.Tier, autoload)})
			log.Printf("plugin %s (%s): below plugins.autoload %s — present, verified, NOT loaded", vid, memo.res.Tier, autoload)
			continue
		}
		desired[vid] = want{found: f, res: memo.res}
	}

	// .
	// .
	a.pluginMu.Lock()
	if a.activeMeta == nil {
		a.activeMeta = make(map[string]activePkgMeta)
	}
	var deactivate []string
	var updates []updateJob
	unverified := map[string]error{}
	for id, meta := range a.activeMeta {
		w, still := desired[id]
		if still && activationIsCurrent(w.pkg, w.res.PackageHash, meta) {
			delete(desired, id)
			continue
		}
		// .
		// .
		// .
		// .
		// .
		// .
		if still && meta.kind == "plugin" {
			if memo, ok := a.pluginVerify[w.pkg]; ok && memo.err == nil {
				updates = append(updates, updateJob{id: id, dir: w.dir, pkg: w.pkg, hash: w.res.PackageHash})
				delete(desired, id)
				continue
			}
		}
		deactivate = append(deactivate, id)
		if memo, ok := a.pluginVerify[meta.pkg]; ok && memo.err != nil {
			unverified[id] = memo.err
		}
	}
	sort.Strings(deactivate)
	a.pluginSkips = skips
	a.pluginMu.Unlock()

	for _, id := range deactivate {
		if why, ok := unverified[id]; ok {
			// .
			// .
			// .
			log.Printf("plugin %s: DEACTIVATED — its package no longer verifies under the current trust status: %v", id, why)
		}
		a.deactivateByID(ctx, id)
	}
	sort.Slice(updates, func(i, j int) bool { return updates[i].id < updates[j].id })
	for _, u := range updates {
		a.updateInPlace(ctx, u, secRoots)
	}

	ids := make([]string, 0, len(desired))
	for id := range desired {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		w := desired[id]
		_, granted := cfg.Plugins.Grants[id]
		a.activateOne(ctx, id, w.dir, w.pkg, w.res.PackageHash, secRoots, granted)
	}

	a.pluginMu.Lock()
	activeIDs := make(map[string]bool, len(a.activeMeta))
	for id := range a.activeMeta {
		activeIDs[id] = true
	}
	a.pluginMu.Unlock()
	for gid := range cfg.Plugins.Grants {
		if !activeIDs[gid] {
			log.Printf("plugins: grant for %q references no active plugin — orphaned or below-threshold policy (harmless)", gid)
		}
	}
	// .
	// .
	a.convergeChannels(ctx)
}

// .
// .
func (a *App) activateOne(ctx context.Context, id, dir, pkg, hash string, secRoots packagefmt.TrustRoots, granted bool) {
	sec, serr := sections.ActivateFromPackage(pkg, secRoots)
	switch {
	case serr == nil:
		if rerr := a.sections.Register(sec); rerr != nil {
			_ = sec.Close()
			log.Printf("section %s: registration REFUSED, package skipped (identity unaffected): %v", pkg, rerr)
			return
		}
		a.pluginMu.Lock()
		a.sectionActs = append(a.sectionActs, sec)
		a.activeMeta[id] = activePkgMeta{dir: dir, pkg: pkg, hash: hash, kind: "section"}
		a.pluginMu.Unlock()
		log.Printf("section %s activated (id %s, slot %s): commands %v topics %v", sec.PackageID, sec.Decl.ID, sec.Decl.Slot, sec.Decl.Commands, sec.Decl.Topics)
		return
	case errors.Is(serr, sections.ErrAssetNotSection):
		a.pluginMu.Lock()
		a.activeMeta[id] = activePkgMeta{dir: dir, pkg: pkg, hash: hash, kind: "asset"}
		a.pluginMu.Unlock()
		log.Printf("plugin %s: kind=asset without section.json — nothing activates for it yet (skipped)", pkg)
		return
	case errors.Is(serr, sections.ErrNotAsset):
		// .
	default:
		log.Printf("section %s: activation REFUSED, package skipped (identity unaffected): %v", pkg, serr)
		return
	}

	actCtx, actCancel := context.WithTimeout(ctx, 30*time.Second)
	ap, err := pluginhost.Activate(actCtx, pkg, a.pluginToolReg, a.pluginOpts)
	actCancel()
	if err != nil {
		log.Printf("plugin %s: activation REFUSED, package skipped (identity unaffected): %v", pkg, err)
		return
	}
	a.pluginMu.Lock()
	a.plugins = append(a.plugins, ap)
	a.activeMeta[id] = activePkgMeta{dir: dir, pkg: pkg, hash: hash, kind: "plugin"}
	a.pluginMu.Unlock()
	a.startSubscriber(ap)
	granted = granted && a.pluginOpts != nil && a.pluginOpts.Broker != nil
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	log.Printf("plugin %s activated (%s, %s, variant %s, %s): tools %v", ap.ID, ap.Tier, ap.Mode, ap.VariantID, activationPosture(ap.Capabilities, granted), ap.ToolNames)
	// .
	// .
	// .
	if g, ok := a.configSnapshot().Plugins.Grants[ap.ID]; ok && len(g.Roots) > 0 && len(g.Hosts) > 0 {
		var roots []string
		for _, r := range g.Roots {
			roots = append(roots, r.Name)
		}
		log.Printf("plugin %s: WARNING — holds readable root(s) %v AND egress to %v: files in those roots can leave this machine through this plugin", ap.ID, roots, g.Hosts)
	}
}

// .
// .
func (a *App) deactivateByID(ctx context.Context, id string) {
	// .
	// .
	// .
	// .
	// .
	// .
	a.pluginMu.Lock()
	meta, ok := a.activeMeta[id]
	if !ok {
		a.pluginMu.Unlock()
		return
	}
	delete(a.activeMeta, id)
	var ap *pluginhost.ActivePlugin
	var sec *sections.Section
	switch meta.kind {
	case "plugin":
		for i, p := range a.plugins {
			if p.ID == id {
				ap = p
				a.plugins = append(a.plugins[:i], a.plugins[i+1:]...)
				break
			}
		}
	case "section":
		for i, s := range a.sectionActs {
			if s.PackageID == id {
				sec = s
				a.sectionActs = append(a.sectionActs[:i], a.sectionActs[i+1:]...)
				break
			}
		}
	}
	a.pluginMu.Unlock()
	if ap != nil {
		a.stopSubscriber(ap.ID)
		dctx, dcancel := context.WithTimeout(ctx, 5*time.Second)
		if err := ap.Deactivate(dctx); err != nil {
			log.Printf("plugin %s: deactivate: %v", id, err)
		}
		dcancel()
		log.Printf("plugin %s deactivated (removed from plugins/ or below the autoload level)", id)
	}
	if sec != nil {
		if a.sections != nil {
			a.sections.Remove(sec.Decl.ID)
		}
		if err := sec.Close(); err != nil {
			log.Printf("section %s: cache removal: %v", id, err)
		}
		log.Printf("section %s deactivated", id)
	}
}

// .
// .
func (a *App) pluginSkipViews() []dashboard.PluginSkipView {
	a.pluginMu.Lock()
	defer a.pluginMu.Unlock()
	out := make([]dashboard.PluginSkipView, 0, len(a.pluginSkips))
	for _, sk := range a.pluginSkips {
		out = append(out, dashboard.PluginSkipView{Dir: sk.Dir, ID: sk.ID, Tier: sk.Tier, Reason: sk.Reason})
	}
	return out
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
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
type updateJob struct{ id, dir, pkg, hash string }

// .
// .
// .
// .
// .
// .
// .
// .
func (a *App) updateInPlace(ctx context.Context, u updateJob, secRoots packagefmt.TrustRoots) {
	a.pluginMu.Lock()
	var oldAp *pluginhost.ActivePlugin
	for _, p := range a.plugins {
		if p.ID == u.id {
			oldAp = p
			break
		}
	}
	a.pluginMu.Unlock()
	if oldAp == nil {
		// .
		// .
		_, granted := a.configSnapshot().Plugins.Grants[u.id]
		a.activateOne(ctx, u.id, u.dir, u.pkg, u.hash, secRoots, granted)
		return
	}
	fromV := oldAp.Version

	actCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	newAp, err := pluginhost.ActivateShadow(actCtx, u.pkg, a.pluginToolReg, a.pluginOpts)
	cancel()
	if err != nil {
		log.Printf("plugin %s: update REFUSED at shadow activation — release %s keeps serving, unchanged: %v", u.id, fromV, err)
		return
	}
	hctx, hcancel := context.WithTimeout(ctx, 10*time.Second)
	herr := newAp.Health(hctx)
	hcancel()
	if herr != nil {
		cctx, ccancel := context.WithTimeout(ctx, 5*time.Second)
		_ = newAp.CloseQuiet(cctx)
		ccancel()
		log.Printf("plugin %s: update to %s FAILED health — rolled back, release %s keeps serving: %v", u.id, newAp.Version, fromV, herr)
		return
	}

	// .
	// .
	if rerr := newAp.Redirect(oldAp); rerr != nil {
		cctx, ccancel := context.WithTimeout(ctx, 5*time.Second)
		_ = newAp.CloseQuiet(cctx)
		ccancel()
		log.Printf("plugin %s: update redirect REFUSED — release %s keeps serving: %v", u.id, fromV, rerr)
		return
	}
	a.pluginMu.Lock()
	for i, p := range a.plugins {
		if p.ID == u.id {
			a.plugins[i] = newAp
			break
		}
	}
	a.activeMeta[u.id] = activePkgMeta{dir: u.dir, pkg: u.pkg, hash: u.hash, kind: "plugin"}
	a.pluginMu.Unlock()
	// .
	// .
	// .
	a.stopSubscriber(u.id)
	a.startSubscriber(newAp)
	log.Printf("plugin %s: updated %s -> %s side by side; draining the predecessor", u.id, fromV, newAp.Version)

	// .
	// .
	// .
	// .
	// .
	// .
	// .
	if oldAp.Pinned() {
		log.Printf("plugin %s: predecessor %s holds an open session — pinned until it closes or its process exits", u.id, fromV)
		a.retire(oldAp)
		go func() {
			released := oldAp.PinReleased()
			untrusted := oldAp.Voice.Untrusted()
			for {
				select {
				case <-released:
					if !a.claimRetiring(oldAp) {
						return
					}
					cctx, ccancel := context.WithTimeout(context.Background(), 5*time.Second)
					if cerr := oldAp.CloseQuiet(cctx); cerr != nil {
						log.Printf("plugin %s: pinned predecessor %s stop: %v", u.id, fromV, cerr)
					}
					ccancel()
					log.Printf("plugin %s: pinned predecessor %s released and stopped", u.id, fromV)
					return
				case <-untrusted:
					// .
					// .
					// .
					// .
					// .
					untrusted = nil
					log.Printf("plugin %s: pinned predecessor %s: session untrusted (%s) — asking the engine to abort", u.id, fromV, oldAp.Voice.FaultReason())
					actx, acancel := context.WithTimeout(context.Background(), 5*time.Second)
					if cerr := oldAp.Voice.Close(actx, "abort", "host fault: "+oldAp.Voice.FaultReason()); cerr != nil {
						log.Printf("plugin %s: pinned predecessor %s: abort not admitted (%v); waiting for the reap", u.id, fromV, cerr)
					}
					acancel()
				}
			}
		}()
		return
	}
	// .
	// .
	if !oldAp.WaitIdle(10 * time.Second) {
		log.Printf("plugin %s: predecessor %s did not go idle within the drain window — stopping it anyway (an in-flight call sees the wall close)", u.id, fromV)
	}
	cctx, ccancel := context.WithTimeout(ctx, 5*time.Second)
	if cerr := oldAp.CloseQuiet(cctx); cerr != nil {
		log.Printf("plugin %s: predecessor %s stop: %v", u.id, fromV, cerr)
	}
	ccancel()
}

func activationIsCurrent(pkg, hash string, meta activePkgMeta) bool {
	return pkg == meta.pkg && hash != "" && hash == meta.hash
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
// .
// .
// .
// .
// .
// .
// .
// .
func (a *App) replacePolicy(cfg Config) {
	if a.pluginOpts == nil || a.pluginOpts.Broker == nil {
		return
	}
	// .
	// .
	// .
	// .
	// .
	// .
	a.pluginOpts.Broker.ReplacePolicy(cfg.Plugins.Grants, cfg.Plugins.AuthProfiles)
}

// .
// .
// .
func trustFingerprint(dir string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "absent"
	}
	var b strings.Builder
	for _, e := range entries {
		info, ierr := e.Info()
		if ierr != nil || e.IsDir() {
			continue
		}
		fmt.Fprintf(&b, "%s:%d:%d|", e.Name(), info.Size(), info.ModTime().UnixNano())
	}
	return b.String()
}

// .
// .
// .
// .
func fetchModel(ctx context.Context, url string, offset int64, w io.Writer) (int64, error) {
	if err := tools.FetchGuard(ctx, url); err != nil {
		return 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("User-Agent", "AII-OS/1.0 (model acquisition)")
	if offset > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
	}
	client := tools.GuardedClient(30*time.Minute, nil, nil)
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusPartialContent:
	case resp.StatusCode == http.StatusOK && offset > 0:
		// .
		// .
		return 0, fmt.Errorf("the server does not resume; the partial download will be discarded and refetched")
	case resp.StatusCode == http.StatusOK:
	default:
		return 0, fmt.Errorf("the model server answered %d", resp.StatusCode)
	}
	return io.Copy(w, resp.Body)
}

// .
// .
// .
func (a *App) retire(ap *pluginhost.ActivePlugin) {
	a.pluginMu.Lock()
	a.retiring = append(a.retiring, ap)
	a.pluginMu.Unlock()
}

// .
// .
func (a *App) claimRetiring(ap *pluginhost.ActivePlugin) bool {
	a.pluginMu.Lock()
	defer a.pluginMu.Unlock()
	for i, r := range a.retiring {
		if r == ap {
			a.retiring = append(a.retiring[:i], a.retiring[i+1:]...)
			return true
		}
	}
	return false
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
// .
func pluginDataRoot(ledgerPath string) string {
	dir := filepath.Dir(ledgerPath)
	if abs, err := filepath.Abs(dir); err == nil {
		return abs
	}
	return dir
}

// .
// .
// .
// .
// .
// .
// .
// .
func activationPosture(capabilities []string, granted bool) string {
	signed := "signed for nothing"
	if len(capabilities) > 0 {
		signed = "signed for " + strings.Join(capabilities, ", ")
	}
	// .
	// .
	// .
	// .
	// .
	var inEffect []string
	for _, c := range capabilities {
		if broker.EnvelopeOnly(c) {
			inEffect = append(inEffect, c)
		}
	}
	if len(inEffect) > 0 {
		signed += fmt.Sprintf(" (%s needs no grant and is in effect)", strings.Join(inEffect, ", "))
	}
	if granted {
		return signed + ", brokered (operator grant active)"
	}
	return signed + ", quarantine, no operator grant"
}
