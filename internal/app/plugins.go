package app

import (
	"context"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/broker"
	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/firewall"
	"github.com/aiii-dot-id/aii-os/internal/fsdir"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/pluginfacility"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
	"github.com/aiii-dot-id/aii-os/internal/store"
	"github.com/aiii-dot-id/aii-os/internal/tools"
	"github.com/aiii-dot-id/aii-os/internal/updates"
)

const WorkerSubcommand = "plugin-worker"

var harnessLane func() (string, []string, error)

func (a *App) buildPluginOptions(st *store.Store, toolReg *tools.Registry, door *ledgerAdapter) (*pluginhost.Options, error) {
	cfg := a.configSnapshot()
	opts := &pluginhost.Options{WebhookURL: a.webhookURL}
	opts.ResourceFacts = measureSelectionResources
	opts.SelectionPolicy = pluginAdmissionPolicy(cfg)
	opts.StorageUpgradeReady = func() error {
		return updates.RequireSettledBootUpdate(identityDataDir(a.configSnapshot().Identity.LedgerPath))
	}
	opts.LegacyStorageOwners = func(ctx context.Context) ([]string, error) {
		var ids []string
		evidence, err := a.pluginFacility().VerifiedPackages(ctx)
		if err != nil {
			return nil, err
		}
		for _, ev := range evidence {
			if res, ok := ev.Handle.(*packagefmt.Result); ok && res != nil && res.Manifest != nil && res.Tier.PublisherProven() && res.Manifest.Kind == "plugin" {
				ids = append(ids, res.Manifest.ID)
			}
		}
		return ids, nil
	}
	opts.NativePlaces = func() []*firewall.Rule {
		current := a.configSnapshot()
		return substrateObjects(current, current.Tools.CWD)
	}
	var err error

	if cfg.Plugins.Runtime.MaxStartupMS != 0 {
		if opts.StartupCeiling, err = cfg.Plugins.Runtime.startupCeiling(); err != nil {
			return nil, fmt.Errorf("plugins.runtime.max_startup_ms: %w", err)
		}
	}
	if opts.Roots.PublisherCertifier, err = packagefmt.PinnedOrShipped(cfg.Plugins.CertifierRoot, packagefmt.KeyTypePublisherCertifier); err != nil {
		return nil, fmt.Errorf("plugins.certifier_root: %w", err)
	}
	if opts.Roots.Reviewer, err = packagefmt.PinnedOrShipped(cfg.Plugins.ReviewerRoot, packagefmt.KeyTypeReviewer); err != nil {
		return nil, fmt.Errorf("plugins.reviewer_root: %w", err)
	}
	if opts.Roots.PlatformRelease, err = packagefmt.PinnedOrShipped(cfg.Plugins.PlatformRoot, packagefmt.KeyTypePlatformRelease); err != nil {
		return nil, fmt.Errorf("plugins.platform_root: %w", err)
	}

	trustDir := filepath.Join(filepath.Dir(cfg.Identity.LedgerPath), "trust")
	a.trustDir, a.trustGuard = trustDir, trustEpochGuard{door: door, st: st}
	a.trustFinger = trustFingerprint(trustDir)
	opts.Roots.Revocation = packagefmt.LoadRevocationStatus(trustDir, opts.Roots, a.trustGuard)
	for _, line := range opts.Roots.Revocation.Describe() {
		logsink.Info("plugins.decision", "%s", line)
	}
	a.catalog.Open(cfg.Plugins.CatalogDir, filepath.Join(filepath.Dir(cfg.Identity.LedgerPath), "plugins-catalog"), opts.Roots.PlatformRelease, a.effectiveCatalogURL)

	if opts.Facilities, err = a.hostFacilities(); err != nil {
		return nil, fmt.Errorf("host facilities: %w", err)
	}
	if opts.WorkerBinary, opts.WorkerArgs, err = a.resolveWorkerBinary(); err != nil {
		return nil, err
	}

	if opts.WorkerBinary == "" {
		logsink.Info("plugins.start", "lane in-process (no supervised worker on this platform)")
	} else {
		logsink.Info("plugins.start", "lane supervised: %s", strings.Join(append([]string{opts.WorkerBinary}, opts.WorkerArgs...), " "))
	}

	opts.Settings = a.pluginSettingsSource
	opts.Acts = actProposer{a}
	if len(cfg.Plugins.Resources) > 0 {
		opts.MemoryMax = make(map[string]uint64, len(cfg.Plugins.Resources))
		opts.StreamMax = make(map[string]int, len(cfg.Plugins.Resources))
		opts.FilesMax = make(map[string]int, len(cfg.Plugins.Resources))
		opts.ReadyTimeout = make(map[string]time.Duration, len(cfg.Plugins.Resources))
		for id, r := range cfg.Plugins.Resources {
			deadline, err := r.startupTimeout()
			if err != nil {
				return nil, fmt.Errorf("plugins.resources.%s.startup_timeout_ms: %w", id, err)
			}
			opts.ReadyTimeout[id] = deadline
			opts.MemoryMax[id] = r.MemoryMaxBytes
			opts.StreamMax[id] = r.StreamMaxBytes
			opts.FilesMax[id] = r.FilesMaxBytes
		}
	}

	dataDir := identityDataDir(cfg.Identity.LedgerPath)
	opts.PluginDataDir = filepath.Join(dataDir, "plugins-data")

	opts.PluginModelsDir = filepath.Join(dataDir, "plugins-models")
	fetch := a.modelFetch
	if fetch == nil {
		fetch = fetchModel
	}
	opts.ModelFetcher = fetch

	opts.PluginRuntimeDir = filepath.Join(dataDir, "plugins-runtime")
	opts.RuntimeFetcher = fetch
	opts.RuntimeLimits = cfg.Plugins.Runtime.TreeLimits()
	opts.RuntimeRootsKept = cfg.Plugins.Runtime.RootsKept
	if a.runtimeRoots == nil {
		a.runtimeRoots = pluginhost.NewRuntimeRoots()
	}
	opts.RuntimeRoots = a.runtimeRoots

	opts.Acquirer = pluginhost.NewAcquirer(pluginhost.AcquirerConfig{
		ModelFetcher: fetch, RuntimeFetcher: fetch,
		Spawn: a.runBackground, Ready: a.materialReady, Changed: a.pluginsChanged,
	})
	{

		catalog, cerr := a.oauthContracts()
		if cerr != nil {
			return nil, cerr
		}
		opts.Broker, err = broker.New(broker.Config{
			Interactions:   a,
			OAuthProviders: catalog,
			Store:          st,
			Grants:         cfg.Plugins.Grants,
			AuthProfiles:   cfg.Plugins.AuthProfiles,
			ObserveFetch:   toolReg.NotifyFetch,

			Voice: voiceObserver{a},

			Embed: providerEmbedder{a},

			InSAFE: func() bool { return a.currentMode() == ModeSafe },

			Sandbox: newPluginSandbox(toolReg, a.ensureRing5Policy(), cfg.Identity.LedgerPath),

			OwnListener: a.ownListener,
			Guard:       a.brokerGuard,
			Transport:   a.brokerTransport,
		})
		if err != nil {
			return nil, err
		}
	}
	return opts, nil
}

func (a *App) pokePluginSweep() {
	select {
	case a.sweepPoke <- struct{}{}:
	default:
	}

	if a.facility != nil {
		a.facility.Poke("app")
	}
}

func (a *App) startPluginSweep(ctx context.Context) {
	a.sweepPoke = make(chan struct{}, 1)

	a.pluginFacility().Attach(ctx)
	a.watchFacility(ctx)

	if acq := a.acquirer(); acq != nil {
		acq.Attach(ctx)
	}

	if err := os.MkdirAll("plugins", 0o750); err != nil {
		logsink.Warn("plugins.error", "cannot create plugins/ (%v) — drop-ins wait for the heartbeat", err)
	}
	w := fsdir.New(ctx, a.gate, "plugins", fsdir.Options{Depth: 1})

	var trustC <-chan struct{}
	if a.trustDir != "" {
		trustC = fsdir.New(ctx, a.gate, a.trustDir, fsdir.Options{}).C
	}
	a.runBackground(func() { a.runPluginSweep(ctx, w.C, trustC) })
}

func (a *App) runPluginSweep(ctx context.Context, packages, trust <-chan struct{}) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-packages:
		case <-trust:
		case <-a.sweepPoke:
		}

		a.rescanPlugins(ctx)
	}
}

func (a *App) discoverPlugins() pluginfacility.Discovery {
	var out pluginfacility.Discovery
	dirs, _ := filepath.Glob(filepath.Join("plugins", "*"))
	sort.Strings(dirs)
	for _, pdir := range dirs {
		if st, err := os.Stat(pdir); err != nil || !st.IsDir() {
			continue
		}
		pkgs, _ := filepath.Glob(filepath.Join(pdir, "*.aiiospkg"))
		if len(pkgs) == 0 {
			continue
		}
		if len(pkgs) > 1 {
			out.Ambiguous = append(out.Ambiguous, pdir)
			continue
		}
		st, err := os.Stat(pkgs[0])
		if err != nil {
			continue
		}
		out.Found = append(out.Found, pluginfacility.Found{Dir: pdir, Package: pkgs[0], Size: st.Size(), MTime: st.ModTime().UnixNano()})
	}
	return out
}

func (a *App) rescanPlugins(ctx context.Context) {
	cfg := a.configSnapshot()
	autoload := cfg.Plugins.Autoload
	minTier, _, tierOK := autoloadTier(autoload)
	if !tierOK {
		logsink.Warn("plugins.refusal", "autoload %q is not a level (none, T0..T3) — using T1", autoload)
		minTier = packagefmt.TierT1
	}

	trustChanged := false
	if a.pluginOpts != nil && a.trustDir != "" {
		if tf := trustFingerprint(a.trustDir); tf != a.trustFinger {
			a.trustFinger = tf
			status := packagefmt.LoadRevocationStatus(a.trustDir, (pluginRuntime{a: a}).roots(), a.trustGuard)
			a.publishPluginRevocations(status)
			for _, line := range status.Describe() {
				logsink.Info("plugins.decision", "trust directory changed — %s", line)
			}
			trustChanged = true
		}
	}

	f := a.pluginFacility()
	f.Rescan(a.pluginPolicy(cfg, minTier, trustChanged))

	wanted := map[string]bool{}
	for _, v := range f.Snapshot().Instances {
		if v.Wanted {
			wanted[v.ID] = true
		}
	}
	_, safe := a.SafeMode()
	if acq := a.acquirer(); acq != nil {
		if safe {

			acq.Keep(map[string]bool{})
		} else {
			acq.Keep(wanted)
		}
	}
	granted := make([]string, 0, len(cfg.Plugins.Grants))
	for gid := range cfg.Plugins.Grants {
		granted = append(granted, gid)
	}
	for _, gid := range orphanedGrants(granted, wanted) {
		logsink.Info("plugins.refusal", "grant for %q references no package this host wants — orphaned or below-threshold policy (harmless)", gid)
	}

	if !safe {
		a.convergeChannels(ctx)
	}
	a.convergeSchedules(ctx)

	a.pluginsChanged()
}

func (a *App) publishPluginRevocations(status *packagefmt.RevocationStatusSet) {
	a.pluginMu.Lock()
	defer a.pluginMu.Unlock()
	a.pluginOpts.Roots.Revocation = status
}

func orphanedGrants(granted []string, wanted map[string]bool) []string {
	var out []string
	for _, gid := range granted {
		if !wanted[gid] {
			out = append(out, gid)
		}
	}
	sort.Strings(out)
	return out
}

func (a *App) pluginSkipViews() []dashboard.PluginSkipView {
	skips := a.pluginFacility().Skips()
	out := make([]dashboard.PluginSkipView, 0, len(skips))
	for _, sk := range skips {
		out = append(out, dashboard.PluginSkipView{Kind: sk.Kind, Dir: sk.Dir, Package: sk.Package, ID: sk.ID, Tier: sk.Tier, Reason: sk.Reason})
	}
	return out
}

func activationIsCurrent(pkg, hash string, meta activePkgMeta) bool {
	return pkg == meta.pkg && hash != "" && hash == meta.hash
}

func (a *App) replacePolicy(cfg Config) {

	if a.toolReg != nil && a.applySubstrate(cfg) {
		a.loadRing5()
	}
	if a.pluginOpts == nil || a.pluginOpts.Broker == nil {
		return
	}

	catalog, err := a.oauthContracts()
	if err != nil {
		logsink.Warn("plugins.refusal", "OAuth configuration: %v", err)
	}
	a.pluginOpts.Broker.ReplacePolicy(cfg.Plugins.Grants, cfg.Plugins.AuthProfiles, catalog)
}

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

		return 0, fmt.Errorf("the server does not resume; the partial download will be discarded and refetched")
	case resp.StatusCode == http.StatusOK:
	default:
		return 0, fmt.Errorf("the model server answered %d", resp.StatusCode)
	}
	return io.Copy(w, resp.Body)
}

func (a *App) retire(ap *pluginhost.ActivePlugin) {
	a.pluginMu.Lock()
	a.retiring = append(a.retiring, ap)
	a.pluginMu.Unlock()
}

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

func activationPosture(capabilities []string, granted bool) string {
	signed := "signed for nothing"
	if len(capabilities) > 0 {
		signed = "signed for " + strings.Join(capabilities, ", ")
	}

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

func (a *App) acquirer() *pluginhost.Acquirer {
	if a.pluginOpts == nil {
		return nil
	}
	return a.pluginOpts.Acquirer
}

func (a *App) forgetAcquisition(id string) {
	if acq := a.acquirer(); acq != nil {
		acq.Forget(id)
	}
}

func (a *App) materialReady(id string) {
	a.rerunPluginSweep()
	a.pluginsChanged()
}

func (a *App) rerunPluginSweep() { a.pokePluginSweep() }

func (a *App) pluginsChanged() {
	if a.dashboard != nil {
		a.dashboard.BroadcastConfig()
	}
}

const (
	lifeStarting = "starting"
	lifeRefused  = "refused"

	lifeRetiring = "retiring"

	lifeHeld = "held"
)

type pluginLifecycle struct {
	note                   string
	version, phase, reason string
	since                  time.Time

	view pluginfacility.InstanceView
}

func (a *App) markPlugin(id, version, phase, reason string) {
	a.pluginMu.Lock()
	if phase == "" {
		delete(a.pluginLife, id)
	} else {
		if a.pluginLife == nil {
			a.pluginLife = map[string]pluginLifecycle{}
		}
		a.pluginLife[id] = pluginLifecycle{version: version, phase: phase, reason: reason, since: time.Now()}
	}
	a.pluginMu.Unlock()
	a.pluginsChanged()
}

func (a *App) RetryPlugin(id string) error {
	if id == "" || strings.ContainsAny(id, "/\\") || strings.Contains(id, "..") {
		return fmt.Errorf("plugins: refusing to retry %q — not a plain plugin id", id)
	}
	if !a.pluginFacility().Retry(id) {
		return fmt.Errorf("plugins: %q is not a plugin this host has — nothing to try again", id)
	}

	if acq := a.acquirer(); acq != nil {

		a.mode.mu.RLock()
		if a.mode.mode != ModeSafe {
			acq.Retry(id)
		}
		a.mode.mu.RUnlock()
	}

	a.rerunPluginSweep()
	logsink.Info("plugins.decision", "retrying %s at the operator's word", id)
	return nil
}

type pendingMark struct{ phase, text string }

func (a *App) pluginPendingViews() []dashboard.PluginPendingView {
	byID := map[string]dashboard.PluginPendingView{}
	if acq := a.acquirer(); acq != nil {
		for _, st := range acq.Snapshot() {
			v := dashboard.PluginPendingView{ID: st.PluginID, Version: st.Version, Phase: st.Phase, Summary: st.Summary(),
				Attempt: st.Attempt, LastError: st.LastError,
				BytesPresent: st.BytesPresent, BytesTotal: st.BytesTotal, FilesPresent: st.FilesPresent, FilesTotal: st.FilesTotal,
				RuntimeDeclared: st.RuntimeDeclared, RuntimePresent: st.RuntimePresent, RuntimeBytes: st.RuntimeBytes}
			if !st.RetryAt.IsZero() {
				v.RetryAt = st.RetryAt.UTC().Format(time.RFC3339)
			}
			for _, ms := range st.Models {
				v.Models = append(v.Models, dashboard.ModelView{Name: ms.Name, Size: ms.Size, Present: ms.Present, Partial: ms.Partial})
			}
			byID[st.PluginID] = v
		}
	}
	a.pluginMu.Lock()
	for id, l := range a.pluginLife {

		pv := dashboard.PluginPendingView{ID: id, Version: l.version, Phase: l.phase, Summary: lifecycleText(l), Since: l.since.UTC().Format(time.RFC3339),
			Refusal: refusalView(l.view.Refusal), Residue: append([]string(nil), l.view.Residue...)}
		if !l.view.RetryAt.IsZero() {
			pv.RetryAt = l.view.RetryAt.UTC().Format(time.RFC3339)
		}
		if l.phase == lifeRetiring {

			pv.Lifecycle = lifecycleView(l.view)
			pv.RetryAt = ""
			if !l.view.CleanupAt.IsZero() {
				pv.CleanupAt = l.view.CleanupAt.UTC().Format(time.RFC3339)
			}
		}
		byID[id] = pv
	}
	a.pluginMu.Unlock()
	if len(byID) == 0 {
		return nil
	}
	out := make([]dashboard.PluginPendingView, 0, len(byID))
	for _, v := range byID {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func lifecycleText(l pluginLifecycle) string {
	if l.phase == lifeRefused {
		return l.reason
	}
	if l.phase == lifeRetiring {
		return "stopping — what it held has not all come back"
	}
	if l.note != "" {
		return l.note
	}
	return "verifying its files and starting it"
}

func (a *App) pendingSummaries() map[string]pendingMark {
	out := map[string]pendingMark{}
	for _, v := range a.pluginPendingViews() {
		out[v.ID] = pendingMark{phase: v.Phase, text: v.Summary}
	}
	return out
}
