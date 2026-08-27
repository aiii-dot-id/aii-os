package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/broker"
	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
	"github.com/aiii-dot-id/aii-os/internal/quiesce"
	"github.com/aiii-dot-id/aii-os/internal/sections"
	"github.com/aiii-dot-id/aii-os/internal/store"
	"github.com/aiii-dot-id/aii-os/internal/tools"
)

// wireCognitive sets up the unconscious: TIME, HEARTBEAT, facilities.
// buildPluginOptions assembles the harness Options from operator
// config: pinned trust roots (loaded through the same validation `aii
// plugin verify` uses — one loader, no drift) and, when any grant
// exists, the capability broker over the identity's store. Zero config
// returns non-nil Options with nothing widened — the T0 deny-all
// posture. An error means the operator's stated intent could not be
// honored; the caller degrades to full quarantine LOUDLY, never
// silently widens.
func (a *App) buildPluginOptions(st *store.Store, toolReg *tools.Registry, door *ledgerAdapter) (*pluginhost.Options, error) {
	cfg := a.configSnapshot()
	opts := &pluginhost.Options{}
	var err error
	if opts.Roots.PublisherCertifier, err = packagefmt.LoadPinnedRoot(cfg.Plugins.CertifierRoot); err != nil {
		return nil, fmt.Errorf("plugins.certifier_root: %w", err)
	}
	if opts.Roots.Reviewer, err = packagefmt.LoadPinnedRoot(cfg.Plugins.ReviewerRoot); err != nil {
		return nil, fmt.Errorf("plugins.reviewer_root: %w", err)
	}
	if opts.Roots.PlatformRelease, err = packagefmt.LoadPinnedRoot(cfg.Plugins.PlatformRoot); err != nil {
		return nil, fmt.Errorf("plugins.platform_root: %w", err)
	}

	// Revocation snapshots (PLUGIN_REVOCATION_DESIGN §2): loaded fresh
	// every boot from <data>/trust/, anti-rollback ledgered
	// (trust.epoch_accepted). Absent snapshots leave their tier
	// unavailable — loud, never wide.
	trustDir := filepath.Join(filepath.Dir(cfg.Identity.LedgerPath), "trust")
	opts.Roots.Revocation = packagefmt.LoadRevocationStatus(trustDir, opts.Roots,
		trustEpochGuard{door: door, st: st})
	for _, line := range opts.Roots.Revocation.Describe() {
		log.Printf("plugins: %s", line)
	}

	// The seam-register wiring (PLATFORM_SEAMS §3): the per-platform
	// facility set (facilities_desktop.go / facilities_mobile.go) and
	// the supervised lane's worker binary. Selection matches variants
	// against these; a wiring failure refuses ALL widening, loudly.
	if opts.Facilities, err = a.hostFacilities(); err != nil {
		return nil, fmt.Errorf("host facilities: %w", err)
	}
	if opts.WorkerBinary, err = a.resolveWorkerBinary(); err != nil {
		return nil, err
	}
	if len(cfg.Plugins.Resources) > 0 {
		opts.MemoryMax = make(map[string]uint64, len(cfg.Plugins.Resources))
		for id, r := range cfg.Plugins.Resources {
			opts.MemoryMax[id] = r.MemoryMaxBytes
		}
	}

	{
		// ALWAYS A BROKER. This was built only when the operator had
		// already granted something, so an identity that started with no
		// grants had no broker — and adding the FIRST grant could never
		// take effect, because there was nothing to tell. A broker with
		// an empty policy denies everything, which is what "no grants"
		// means anyway.
		//
		// Brokered fetches feed the SAME observation seam builtin
		// web_fetch feeds (NotifyFetch), so plugin-fetched URLs earn
		// provenance on the identity's citation ladder (H3).
		opts.Broker, err = broker.New(broker.Config{
			Store:        st,
			Grants:       cfg.Plugins.Grants,
			AuthProfiles: cfg.Plugins.AuthProfiles,
			ObserveFetch: toolReg.NotifyFetch,
			// The host decides what an utterance becomes; the broker
			// only decides whether the plugin may speak at all.
			Voice: voiceObserver{a},
			// SAFE REFUSES EVERY OBSERVATION WHILE IT HOLDS. A voice
			// plugin is push-only — it proposes what it heard and the
			// host decides what that becomes — so this is the only
			// place SAFE has to reach to take the microphone away.
			InSAFE: func() bool { return a.currentMode() == ModeSafe },
		})
		if err != nil {
			return nil, err
		}
	}
	return opts, nil
}

// pokePluginSweep kicks the runtime sweep without waiting for a tick.
func (a *App) pokePluginSweep() {
	select {
	case a.sweepPoke <- struct{}{}:
	default:
	}
}

// startPluginSweep runs the runtime converger on a governed ticker —
// parked while backgrounded (the battery law), poked on policy changes.
func (a *App) startPluginSweep(ctx context.Context) {
	a.sweepPoke = make(chan struct{}, 1)
	tk := quiesce.NewTicker(a.gate, 4*time.Second)
	a.runBackground(func() {
		defer tk.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tk.C:
			case <-a.sweepPoke:
			}
			if _, safe := a.SafeMode(); safe {
				continue // no activation churn while the record is frozen
			}
			a.convergePlugins(ctx)
		}
	})
}

// convergePlugins reconciles the active plugin/section set against
// plugins/ and plugins.autoload — both directions, one code path for
// boot and runtime. Identity comes from VERIFIED evidence; duplicate
// verified ids refuse; a name/id mismatch is a notice; verification
// failure refuses at every level (it is not T0).
func (a *App) convergePlugins(ctx context.Context) {
	cfg := a.configSnapshot()
	autoload := cfg.Plugins.Autoload
	minTier, loadNone, tierOK := autoloadTier(autoload)
	if !tierOK {
		log.Printf("plugins: autoload %q is not a level (none, T0..T3) — using T1", autoload)
		minTier, loadNone = packagefmt.TierT1, false
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
			continue // legal: a state-only or not-yet-filled dir
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
	unchanged := fp == a.pluginFinger
	a.pluginFinger = fp
	a.pluginMu.Unlock()
	if unchanged {
		return
	}

	// Ambiguous dirs (logged once per change), then verify with memo.
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
	desired := map[string]want{} // verified id -> pkg
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

	// Reconcile: deactivate what is active but no longer desired (or
	// whose package bytes changed), then activate the new arrivals.
	a.pluginMu.Lock()
	if a.activeMeta == nil {
		a.activeMeta = make(map[string]activePkgMeta)
	}
	var deactivate []string
	for id, meta := range a.activeMeta {
		w, still := desired[id]
		if still && activationIsCurrent(w.pkg, w.size, w.mtime, meta) {
			delete(desired, id) // already active, unchanged
			continue
		}
		deactivate = append(deactivate, id)
	}
	sort.Strings(deactivate)
	a.pluginSkips = skips
	a.pluginMu.Unlock()

	for _, id := range deactivate {
		a.deactivateByID(ctx, id)
	}
	ids := make([]string, 0, len(desired))
	for id := range desired {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		w := desired[id]
		_, granted := cfg.Plugins.Grants[id]
		a.activateOne(ctx, id, w.dir, w.pkg, w.size, w.mtime, secRoots, granted)
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
	// A channel adapter installed here starts listening here. The sweep
	// owns the active set; nothing else needs to watch for one appearing.
	a.convergeChannels(ctx)
}

// activateOne runs one package through the existing section/plugin
// lanes and records what it activated from.
func (a *App) activateOne(ctx context.Context, id, dir, pkg string, size, mtime int64, secRoots packagefmt.TrustRoots, granted bool) {
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
		a.activeMeta[id] = activePkgMeta{dir: dir, pkg: pkg, size: size, mtime: mtime, kind: "section"}
		a.pluginMu.Unlock()
		log.Printf("section %s activated (id %s, slot %s): commands %v topics %v", sec.PackageID, sec.Decl.ID, sec.Decl.Slot, sec.Decl.Commands, sec.Decl.Topics)
		return
	case errors.Is(serr, sections.ErrAssetNotSection):
		a.pluginMu.Lock()
		a.activeMeta[id] = activePkgMeta{dir: dir, pkg: pkg, size: size, mtime: mtime, kind: "asset"}
		a.pluginMu.Unlock()
		log.Printf("plugin %s: kind=asset without section.json — nothing activates for it yet (skipped)", pkg)
		return
	case errors.Is(serr, sections.ErrNotAsset):
		// kind=plugin: the harness lane.
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
	a.activeMeta[id] = activePkgMeta{dir: dir, pkg: pkg, size: size, mtime: mtime, kind: "plugin"}
	a.pluginMu.Unlock()
	granted = granted && a.pluginOpts != nil && a.pluginOpts.Broker != nil
	posture := "quarantine, zero capabilities"
	if granted {
		posture = "brokered (operator grant active)"
	}
	log.Printf("plugin %s activated (%s, %s, variant %s, %s): tools %v", ap.ID, ap.Tier, ap.Mode, ap.VariantID, posture, ap.ToolNames)
}

// deactivateByID tears one activation down — the uninstall/downgrade
// half of convergence.
func (a *App) deactivateByID(ctx context.Context, id string) {
	// NOTHING IS TORN DOWN HERE, because a deactivated plugin holds
	// nothing. It used to hold audio streams, and teardown had to live
	// in Binding.Close rather than here so that every path reached it —
	// a copy in this function covered only plugins the App deactivates.
	// Binding.Close still ends the activation; there is simply no
	// carrier left for it to close.
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

// pluginSkipViews snapshots the below-threshold set for the dashboard —
// the sweep goroutine rewrites the slice, so copy under the lock.
func (a *App) pluginSkipViews() []dashboard.PluginSkipView {
	a.pluginMu.Lock()
	defer a.pluginMu.Unlock()
	out := make([]dashboard.PluginSkipView, 0, len(a.pluginSkips))
	for _, sk := range a.pluginSkips {
		out = append(out, dashboard.PluginSkipView{Dir: sk.Dir, ID: sk.ID, Tier: sk.Tier, Reason: sk.Reason})
	}
	return out
}

// updateStateView surfaces the checker's current state for the dashboard
// status view (R70). Returns nil when the checker hasn't run yet — the

// activationIsCurrent reports whether what is running is still what is
// wanted: the same package bytes AND the same authority.
//
// AUTHORITY IS NOT PART OF THIS. A grant change briefly lived here as a
// fingerprint, which was both inert — convergence returns early when no
// package changed, which is every configuration reload — and wrong for
// the model: authority is computed per invocation and never retained,
// so a grant change is not a new activation, it is the next invocation
// getting a different answer. Package and signature changes own
// reactivation. See App.replacePolicy.
func activationIsCurrent(pkg string, size, mtime int64, meta activePkgMeta) bool {
	return pkg == meta.pkg && size == meta.size && mtime == meta.mtime
}

// replacePolicy installs the operator's new answer and closes what it
// withdrew.
//
// THIS RUNS ON EVERY RELOAD, not inside plugin convergence. Convergence
// returns early when no package changed — which is every ordinary
// configuration reload — so anything about authority that lived there
// was never reached. Authority is not a property of a package.
//
// GRANTS AND AUTH PROFILES GO TOGETHER because they are one generation
// of one file: a grant naming a credential handle and the profile that
// defines it must never come from different reads.
//
// AND WITHDRAWAL CLOSES SYNCHRONOUSLY. Denying the next invocation is
// enough for a hostcall, which asks every time — and it is not enough
// for a stream, which is already open and already carrying the room's
// audio. A plugin that loses voice loses its microphone in the same
// breath, not at the next thing it happens to ask.
func (a *App) replacePolicy(cfg Config) {
	if a.pluginOpts == nil || a.pluginOpts.Broker == nil {
		return
	}
	// A WITHDRAWN GRANT NEEDS NO TEARDOWN. Every voice.observe consults
	// the live grant through Binding.grant(), so withdrawing it in
	// config is already the whole of the enforcement: the next
	// observation is refused, and there is no held thing to hunt down.
	// This used to close audio streams a plugin had attached, which is
	// what made the diff worth computing under the replacement's lock.
	a.pluginOpts.Broker.ReplacePolicy(cfg.Plugins.Grants, cfg.Plugins.AuthProfiles)
}
