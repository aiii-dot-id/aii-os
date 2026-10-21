package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/firewall"
	"github.com/aiii-dot-id/aii-os/internal/plugincatalog"
	"github.com/aiii-dot-id/aii-os/internal/tools"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/atomicfile"
	"github.com/aiii-dot-id/aii-os/internal/broker"
	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
	"github.com/aiii-dot-id/aii-os/internal/version"
)

const maxCatalogPackageBytes = 512 << 20

type packageFetch func(ctx context.Context, url string, w io.Writer) (int64, error)

func (a *App) effectiveCatalogURL() string {
	if u := strings.TrimSpace(a.configSnapshot().Plugins.CatalogURL); u != "" {
		return u
	}
	return plugincatalog.DefaultCatalogURL
}

func (a *App) catalogState() (url, fetchedAt, refusal string) {
	url = a.effectiveCatalogURL()
	fetchedAt, refusal = a.catalog.State()
	return
}

func (a *App) catalogChanged() {
	a.dashboard.BroadcastStatus()
	a.dashboard.BroadcastConfig()
}

func (a *App) pluginsState(c *Config) dashboard.PluginsState {
	url, at, refusal := a.catalogState()
	views := a.catalogViews()
	state := dashboard.PluginsState{
		Autoload: c.Plugins.Autoload, Skips: a.pluginSkipViews(), Pending: a.pluginPendingViews(), Installed: a.pluginViews(c), Catalog: views,
		CatalogURL: url, CatalogDir: c.Plugins.CatalogDir, CatalogFetchedAt: at, CatalogError: refusal,
		CatalogUpdates: countUpdates(views),
		Runtime: &dashboard.RuntimeLimitsView{
			MaxInstalledBytes: c.Plugins.Runtime.MaxInstalledBytes, MaxFiles: c.Plugins.Runtime.MaxFiles,
			MaxFileBytes: c.Plugins.Runtime.MaxFileBytes, MaxCompressedBytes: c.Plugins.Runtime.MaxCompressedBytes,
			MaxDepth: c.Plugins.Runtime.MaxDepth, RootsKept: c.Plugins.Runtime.RootsKept,
			MaxStartupMS: c.Plugins.Runtime.MaxStartupMS,
		},
		AuthProfiles: a.authProfileViews(c), Providers: a.providerViews(),
		Machine: a.machineView(),
	}
	state.Stored, state.StoredError = a.pluginStoredViews()
	state.StoredReplacedDays = int(c.Plugins.Runtime.replacedMemoryWindow() / (24 * time.Hour))
	if said := a.storedRemoved.Load(); said != nil {
		state.StoredRemoved = *said
	}
	a.markStoredInstalled(state.Stored, state.Pending)
	return state
}

func (a *App) pluginStoredViews() ([]dashboard.PluginStoredView, string) {
	if a.store == nil {
		return nil, ""
	}
	stored, err := a.store.PluginsStored()
	if err != nil {
		return nil, fmt.Sprintf("what plugins have stored here could not be read: %v", err)
	}
	views := make([]dashboard.PluginStoredView, 0, len(stored))
	for _, p := range stored {
		views = append(views, dashboard.PluginStoredView{ID: p.PluginID, Memories: p.Memories, Replaced: p.Replaced, Keys: p.Keys})
	}
	return views, ""
}

func (a *App) markStoredInstalled(views []dashboard.PluginStoredView, pending []dashboard.PluginPendingView) {
	a.pluginMu.Lock()
	active := make(map[string]bool, len(a.plugins))
	for _, p := range a.plugins {
		active[p.ID] = true
	}
	a.pluginMu.Unlock()
	phase := make(map[string]string, len(pending))
	for _, p := range pending {
		phase[p.ID] = p.Phase
	}
	for i := range views {
		views[i].Installed, views[i].Pending = active[views[i].ID], phase[views[i].ID]
	}
}

func (a *App) PluginStoredPage(req dashboard.PluginStoredRequest) (*dashboard.PluginStoredPage, error) {
	if a.store == nil {
		return nil, errors.New("plugins: there is no identity database to read from yet")
	}
	stamp := func(t time.Time) string {
		if t.IsZero() {
			return ""
		}
		return t.UTC().Format(time.RFC3339)
	}
	page := &dashboard.PluginStoredPage{Plugin: req.Plugin, Kind: req.Kind, Before: req.Before}
	switch req.Kind {
	case "memories":
		rows, total, next, err := a.store.PluginStoredMemories(req.Plugin, req.Before)
		if err != nil {
			return nil, fmt.Errorf("plugins: what %s remembered could not be read: %w", req.Plugin, err)
		}
		page.Total, page.Next = total, next
		for _, m := range rows {
			row := dashboard.PluginStoredMemory{ID: m.ID, Text: m.Text, StoredAt: stamp(m.CreatedAt), Temporary: m.Temp}
			if m.SupersededBy != "" {
				row.ReplacedBy, row.ReplacedAt = m.SupersededBy, stamp(m.UpdatedAt)
			}
			page.Memories = append(page.Memories, row)
		}
	case "keys":
		rows, total, next, err := a.store.PluginStoredKeys(req.Plugin, req.Before)
		if err != nil {
			return nil, fmt.Errorf("plugins: the keys %s stored could not be read: %w", req.Plugin, err)
		}
		page.Total, page.Next = total, next
		for _, k := range rows {
			page.Keys = append(page.Keys, dashboard.PluginStoredKey{Key: k.Key, Value: k.Value, StoredAt: stamp(k.UpdatedAt),
				Temporary: k.Temp, DeletionMarker: k.DeletionMarker})
		}
	default:
		return nil, fmt.Errorf("plugins: what a plugin stored is read as memories or keys, not %q", req.Kind)
	}
	return page, nil
}

func storedCounts(memories, replaced, keys int) string {
	count := func(n int, one, many string) string {
		if n == 1 {
			return "1 " + one
		}
		return fmt.Sprintf("%d %s", n, many)
	}
	said := count(memories, "memory", "memories")
	if replaced > 0 {
		said += fmt.Sprintf(" (%d of them replaced)", replaced)
	}
	return said + " and " + count(keys, "key", "keys")
}

func (a *App) RemovePluginStored(id string) error {
	if id == "" {
		return errors.New("plugins: name the plugin whose stored data is to be removed")
	}
	if a.store == nil {
		return errors.New("plugins: there is no identity database to remove from yet")
	}
	removed, err := a.store.RemovePluginStored(id)
	if err != nil {
		return fmt.Errorf("plugins: what %s stored was not removed: %w", id, err)
	}
	said := fmt.Sprintf("Removed what %s stored in this identity's database: %s.",
		id, storedCounts(removed.Memories, removed.Replaced, removed.Keys))
	if removed.Memories == 0 && removed.Keys == 0 {
		said = fmt.Sprintf("%s had nothing stored in this identity's database; nothing was removed.", id)
	}
	a.storedRemoved.Store(&said)
	logsink.Info("catalog.end", "%s", said)

	a.pluginsChanged()
	return nil
}

func countUpdates(views []dashboard.CatalogEntryView) int {
	n := 0
	for _, v := range views {
		if v.UpdateAvailable {
			n++
		}
	}
	return n
}

func (a *App) catalogUpdateCount() int { return countUpdates(a.catalogViews()) }

type installedRelease struct{ version, tier, needs string }

func (a *App) catalogViews() []dashboard.CatalogEntryView {
	cat := a.catalog.Current()
	if cat == nil {
		return nil
	}
	needs := map[string]string{}
	for _, v := range a.pluginFacility().Snapshot().Instances {
		if s := latestSelection(v); s != nil {
			needs[v.ID] = s.Needs
		}
	}
	a.pluginMu.Lock()
	installed := make(map[string]installedRelease, len(a.plugins))
	for _, p := range a.plugins {
		installed[p.ID] = installedRelease{version: p.Version, tier: p.Tier.String(), needs: needs[p.ID]}
	}
	a.pluginMu.Unlock()
	return catalogViewsFor(cat, installed, a.pendingSummaries())
}

func catalogViewsFor(cat *plugincatalog.Index, installed map[string]installedRelease, pending map[string]pendingMark) []dashboard.CatalogEntryView {
	out := make([]dashboard.CatalogEntryView, 0, len(cat.Plugins))
	for _, e := range cat.Plugins {
		_, pkg, _ := cat.Select(e.ID)
		v := dashboard.CatalogEntryView{ID: e.ID, Version: e.Version, Tier: e.Tier, Summary: e.Summary, Available: pkg != nil,
			Description: e.Description, Publisher: e.Publisher, Homepage: e.Homepage, License: e.License,
			Title: e.Title, Category: e.Category, Keywords: e.Keywords, Updated: e.Updated}
		if pkg != nil {
			v.Size = pkg.Size
		}
		if ir, ok := installed[e.ID]; ok {
			v.Installed, v.Needs = true, ir.needs
			v.InstalledVersion, v.InstalledTier = ir.version, ir.tier
			v.UpdateAvailable = pkg != nil && plugincatalog.NewerVersion(e.Version, ir.version)
		}

		if pm, ok := pending[e.ID]; ok {
			v.Pending, v.PendingText = pm.phase, pm.text
			if pm.needs != "" {
				v.Needs = pm.needs
			}
		}

		if ok, why := e.SupportedBy(version.Authored()); !ok {
			v.Available, v.Requires = false, why
		} else if pkg != nil {

			if ok, why := pkg.RunsOn(systemVersion()); !ok {
				v.Available, v.Requires = false, why
			}
		}
		out = append(out, v)
	}
	return out
}

type CatalogInstallRefusal struct {
	ID      string
	Reason  string
	Cause   error
	Cleanup error
}

func (r *CatalogInstallRefusal) Error() string {
	s := fmt.Sprintf("plugins: %s not installed: %v", r.ID, r.Cause)
	if r.Cleanup != nil {
		s += "; AND what the attempt made could not be removed: " + r.Cleanup.Error()
	}
	return s
}

func (r *CatalogInstallRefusal) Unwrap() error { return r.Cause }

const reservedIDRefusal = "%q begins with %q, a word the host uses for its own parts: a plugin's words reach the identity marked \"plugin <id> says\", and under this id they would read as the host speaking. A new plugin is not installed under it; one already installed under such an id keeps working"

func slotHoldsPackage(slot string) bool {
	m, _ := filepath.Glob(filepath.Join(slot, "*.aiiospkg"))
	return len(m) > 0
}

func plainName(s string) bool {
	return s != "" && s != "." && !strings.ContainsAny(s, "/\\") && !strings.Contains(s, "..")
}

func (a *App) InstallFromCatalog(ctx context.Context, id string) error {
	cat := a.catalog.Current()
	if cat == nil {
		return &CatalogInstallRefusal{ID: id, Reason: "no_catalog", Cause: errors.New("no catalog is configured or loaded")}
	}
	entry, pkg, err := cat.Select(id)
	if err != nil {
		return &CatalogInstallRefusal{ID: id, Reason: "not_offered", Cause: err}
	}
	if ok, why := pkg.RunsOn(systemVersion()); !ok {
		return &CatalogInstallRefusal{ID: id, Reason: "not_offered", Cause: errors.New(why)}
	}
	if !plainName(id) || !plainName(entry.Version) {
		return &CatalogInstallRefusal{ID: id, Reason: "not_plain", Cause: fmt.Errorf("the catalog names version %q of %q, and a slot takes plain names only", entry.Version, id)}
	}

	if word := tools.ReservedPluginID(id); word != "" && !slotHoldsPackage(filepath.Join("plugins", id)) {
		return &CatalogInstallRefusal{ID: id, Reason: "reserved_id", Cause: fmt.Errorf(reservedIDRefusal, id, word)}
	}
	tmp, err := a.fetchVerified(ctx, id, pkg)
	if err != nil {
		return err
	}
	slot := filepath.Join("plugins", id)
	final := filepath.Join(slot, id+"-"+entry.Version+".aiiospkg")
	err = os.Mkdir(slot, 0o750)
	made := err == nil
	if made {
		err = atomicfile.SyncDir("plugins")
	} else if errors.Is(err, fs.ErrExist) {
		err = nil
	}
	published := false
	if err == nil {
		published, err = atomicfile.Replace(tmp, final)
	}
	if !published {
		r := &CatalogInstallRefusal{ID: id, Reason: "place", Cause: err}
		var left []error
		if rerr := os.Remove(tmp); rerr != nil {
			left = append(left, rerr)
		}
		if made {
			if rerr := os.Remove(slot); rerr != nil {
				left = append(left, rerr)
			}
		}
		r.Cleanup = errors.Join(left...)
		return r
	}

	var after error
	if err != nil {
		after = fmt.Errorf("its directory sync failed, so it may not survive a power loss: %w", err)
	}
	if err := keepOnly(slot, final); err != nil {
		after = errors.Join(after, err)
	}

	if acq := a.acquirer(); acq != nil {
		acq.Reselect(id)
	}

	granted, gerr := a.grantAtInstall(id, final, made)
	if gerr != nil {
		after = errors.Join(after, fmt.Errorf("its grants were not recorded: %w", gerr))
	}
	a.pokePluginSweep()
	if after != nil {
		return fmt.Errorf("plugins: %s %s is in place, but: %w", id, entry.Version, after)
	}
	if len(granted) > 0 {
		logsink.Info("catalog.end", "installed %s %s from the catalog (%s), granted %s by the install; the sweep will verify and activate it", id, entry.Version, pkg.URL, strings.Join(granted, ", "))
		return nil
	}
	logsink.Info("catalog.end", "installed %s %s from the catalog (%s); the sweep will verify and activate it", id, entry.Version, pkg.URL)
	return nil
}

func (a *App) grantAtInstall(id, pkgPath string, newSlot bool) ([]string, error) {
	if !newSlot {
		return nil, nil
	}
	res, err := pluginhost.Verify(pkgPath, a.pluginOptions())
	if err != nil || res == nil || res.Manifest == nil {
		return nil, nil
	}
	var asked []string
	if err := json.Unmarshal(res.Manifest.CapabilityEnvelope, &asked); err != nil {
		return nil, nil
	}
	changes := map[string]any{}
	var granted []string
	for _, c := range asked {
		if field := broker.InstallGrant(c); field != "" {
			changes["plugins.grants."+id+"."+field] = true
			granted = append(granted, field)
		}
	}
	if len(changes) == 0 {
		return nil, nil
	}
	sort.Strings(granted)
	if _, err := a.applyConfigChange(changes); err != nil {
		return nil, err
	}
	return granted, nil
}

func (a *App) fetchVerified(ctx context.Context, id string, pkg *plugincatalog.CatalogPackage) (string, error) {
	if err := os.MkdirAll("plugins", 0o750); err != nil {
		return "", &CatalogInstallRefusal{ID: id, Reason: "place", Cause: err}
	}
	tmp, err := os.CreateTemp("plugins", ".download-*")
	if err != nil {
		return "", &CatalogInstallRefusal{ID: id, Reason: "place", Cause: err}
	}
	fetch := a.pkgFetch
	if fetch == nil {
		fetch = a.fetchPackage
	}
	h := sha256.New()
	n, ferr := fetch(ctx, pkg.URL, io.MultiWriter(tmp, h))
	got := "sha256:" + hex.EncodeToString(h.Sum(nil))
	r := &CatalogInstallRefusal{ID: id}
	switch {
	case ferr != nil:
		r.Reason, r.Cause = "fetch", fmt.Errorf("downloading %s: %w", pkg.URL, ferr)
	case n > maxCatalogPackageBytes:
		r.Reason, r.Cause = "size", fmt.Errorf("the package exceeds the %d-byte install ceiling", maxCatalogPackageBytes)
	case pkg.Size != 0 && n != pkg.Size:
		r.Reason, r.Cause = "size", fmt.Errorf("downloaded %d bytes, the catalog declares %d", n, pkg.Size)
	case got != pkg.SHA256:
		r.Reason, r.Cause = "hash", fmt.Errorf("the package hashes to %s, the catalog declares %s", got, pkg.SHA256)
	default:
		if err := tmp.Sync(); err != nil {
			r.Reason, r.Cause = "place", err
		}
	}
	if err := tmp.Close(); err != nil && r.Cause == nil {
		r.Reason, r.Cause = "place", err
	}
	if r.Cause == nil {
		return tmp.Name(), nil
	}
	if err := os.Remove(tmp.Name()); err != nil {
		r.Cleanup = err
	}
	return "", r
}

func keepOnly(slot, final string) error {
	entries, err := os.ReadDir(slot)
	if err != nil {
		return err
	}
	var errs []error
	removed := false
	for _, e := range entries {
		p := filepath.Join(slot, e.Name())
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".aiiospkg") || p == final {
			continue
		}
		if err := os.Remove(p); err != nil {
			errs = append(errs, fmt.Errorf("the earlier release %s remains beside it, and the sweep refuses a slot holding two: %w", e.Name(), err))
			continue
		}
		removed = true
	}
	if removed {
		if err := atomicfile.SyncDir(slot); err != nil {
			errs = append(errs, fmt.Errorf("the earlier release's removal is not yet durable: %w", err))
		}
	}
	return errors.Join(errs...)
}

func (a *App) UninstallPlugin(id string) error {
	if !plainName(id) {
		return fmt.Errorf("plugins: refusing to uninstall %q — not a plain plugin id", id)
	}
	dir := filepath.Join("plugins", id)
	if _, err := os.Stat(dir); err != nil {
		return fmt.Errorf("plugins: %s is not installed", id)
	}
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	a.pokePluginSweep()
	logsink.Info("catalog.end", "uninstalled %s; the sweep will deactivate it", id)
	return nil
}

func (a *App) fetchPackage(ctx context.Context, url string, w io.Writer) (int64, error) {
	if err := firewall.FetchGuard(ctx, url); err != nil {
		return 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("User-Agent", "AII-OS/1.0 (plugin acquisition)")
	client := firewall.GuardedClient(30*time.Minute, nil, nil)
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("the catalog host answered %d", resp.StatusCode)
	}
	return io.Copy(w, io.LimitReader(resp.Body, maxCatalogPackageBytes+1))
}

func (a *App) wirePluginHooks(h *dashboard.WSHandler) {
	h.SetPluginKey = a.SetPluginSecret
	h.ClearPluginKey = a.ClearPluginSecret
	h.SettingChoices = a.PluginSettingChoices
	h.PluginStored = a.PluginStoredPage
	h.PluginAct = func(req dashboard.PluginAction) error {
		switch req.Action {
		case "install":
			return a.InstallFromCatalog(a.lifetime(), req.ID)
		case "uninstall":
			return a.UninstallPlugin(req.ID)
		case "retry":
			return a.RetryPlugin(req.ID)
		case "confirm", "deny":
			return a.decideAct(a.lifetime(), req.ID, req.Act, req.Action == "confirm")
		case "always":
			return a.alwaysAct(a.lifetime(), req.ID, req.Act)
		case "machine_update":
			return a.forceMachineUpdate()
		case "remove_stored":
			return a.RemovePluginStored(req.ID)
		default:
			return fmt.Errorf("unknown plugin action %q", req.Action)
		}
	}
	h.CatalogRefresh = func() error {
		ctx, cancel := context.WithTimeout(a.lifetime(), 2*time.Minute)
		defer cancel()

		err := a.catalog.Refresh(ctx)
		a.catalogChanged()
		return err
	}
}
