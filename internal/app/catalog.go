package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/firewall"
	"github.com/aiii-dot-id/aii-os/internal/plugincatalog"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/atomicfile"
	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
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
	return dashboard.PluginsState{
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
	}
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

type installedRelease struct{ version, tier string }

func (a *App) catalogViews() []dashboard.CatalogEntryView {
	cat := a.catalog.Current()
	if cat == nil {
		return nil
	}
	a.pluginMu.Lock()
	installed := make(map[string]installedRelease, len(a.plugins))
	for _, p := range a.plugins {
		installed[p.ID] = installedRelease{version: p.Version, tier: p.Tier.String()}
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
			v.Installed = true
			v.InstalledVersion, v.InstalledTier = ir.version, ir.tier
			v.UpdateAvailable = pkg != nil && plugincatalog.NewerVersion(e.Version, ir.version)
		}

		if pm, ok := pending[e.ID]; ok {
			v.Pending, v.PendingText = pm.phase, pm.text
		}

		if ok, why := e.SupportedBy(version.Authored()); !ok {
			v.Available, v.Requires = false, why
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
	if !plainName(id) || !plainName(entry.Version) {
		return &CatalogInstallRefusal{ID: id, Reason: "not_plain", Cause: fmt.Errorf("the catalog names version %q of %q, and a slot takes plain names only", entry.Version, id)}
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
	a.pokePluginSweep()
	if after != nil {
		return fmt.Errorf("plugins: %s %s is in place, but: %w", id, entry.Version, after)
	}
	logsink.Info("catalog.end", "installed %s %s from the catalog (%s); the sweep will verify and activate it", id, entry.Version, pkg.URL)
	return nil
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
