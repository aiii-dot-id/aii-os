package app

// .
// .
// .
// .
// .
// .
// .
// .
// .

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
	"github.com/aiii-dot-id/aii-os/internal/sigenvelope"
	"github.com/aiii-dot-id/aii-os/internal/tools"
	"github.com/aiii-dot-id/aii-os/internal/version"
)

// .
// .
const maxCatalogPackageBytes = 512 << 20

// .
// .
// .
type packageFetch func(ctx context.Context, url string, w io.Writer) (int64, error)

// .
// .
// .
// .
func (a *App) loadCatalog(root *sigenvelope.PublicKeyEnvelope) {
	if a.catalogDir != "" {
		cat, err := pluginhost.LoadCatalog(a.catalogDir, root)
		if err != nil {
			log.Printf("plugins: catalog at %s unavailable: %v", a.catalogDir, err)
			return
		}
		a.adoptCatalog(cat, "")
		log.Printf("plugins: catalog loaded from %s — %d plugin(s) available", a.catalogDir, len(cat.Plugins))
		return
	}
	// .
	// .
	// .
	if a.catalogCache == "" {
		return
	}
	cat, err := pluginhost.LoadCatalog(a.catalogCache, root)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			log.Printf("plugins: the kept catalog at %s is unusable: %v", a.catalogCache, err)
		}
		return
	}
	a.adoptCatalog(cat, "")
	log.Printf("plugins: catalog loaded from the last verified fetch — %d plugin(s); the URL is refreshed when online", len(cat.Plugins))
}

func (a *App) adoptCatalog(cat *pluginhost.Catalog, fetchedAt string) {
	a.catalogMu.Lock()
	a.catalog = cat
	if fetchedAt != "" {
		a.catalogAt = fetchedAt
	}
	a.catalogErr = ""
	a.catalogMu.Unlock()
}

// .
// .
const (
	maxCatalogIndexBytes = 4 << 20
	maxCatalogSigBytes   = 64 << 10
	catalogRefreshEvery  = time.Hour
)

// .
// .
// .
// .
// .
func (a *App) refreshCatalog(ctx context.Context) error {
	url := a.effectiveCatalogURL()
	if a.catalogRoot == nil {
		return a.catalogRefused(errors.New("no platform_release root is pinned — a fetched catalog cannot be verified"))
	}
	fetch := a.catalogFetch
	if fetch == nil {
		fetch = a.fetchSmall
	}
	md, err := fetch(ctx, url, maxCatalogIndexBytes)
	if err != nil {
		return a.catalogRefused(fmt.Errorf("fetching the index: %w", err))
	}
	sig, err := fetch(ctx, url+".sig", maxCatalogSigBytes)
	if err != nil {
		return a.catalogRefused(fmt.Errorf("fetching the index signature: %w", err))
	}
	cat, err := pluginhost.ParseCatalog(md, sig, a.catalogRoot)
	if err != nil {
		return a.catalogRefused(err)
	}
	a.adoptCatalog(cat, time.Now().UTC().Format(time.RFC3339))
	if a.catalogCache != "" {
		if err := writeCatalogCache(a.catalogCache, md, sig); err != nil {
			log.Printf("plugins: the verified catalog could not be kept at %s: %v", a.catalogCache, err)
		}
	}
	log.Printf("plugins: catalog refreshed from %s — %d plugin(s) available", url, len(cat.Plugins))
	return nil
}

func (a *App) catalogRefused(err error) error {
	a.catalogMu.Lock()
	a.catalogErr = err.Error()
	a.catalogMu.Unlock()
	log.Printf("plugins: catalog refresh: %v (the last good index stays)", err)
	return err
}

// .
// .
func writeCatalogCache(dir string, md, sig []byte) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	for _, f := range []struct {
		name string
		data []byte
	}{{pluginhost.CatalogFile, md}, {pluginhost.CatalogSigFile, sig}} {
		tmp := filepath.Join(dir, "."+f.name+".tmp")
		if err := os.WriteFile(tmp, f.data, 0o600); err != nil {
			return err
		}
		if err := os.Rename(tmp, filepath.Join(dir, f.name)); err != nil {
			os.Remove(tmp)
			return err
		}
	}
	return nil
}

// .
// .
func (a *App) fetchSmall(ctx context.Context, url string, max int64) ([]byte, error) {
	if err := tools.FetchGuard(ctx, url); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "AII-OS/1.0 (plugin catalog)")
	client := tools.GuardedClient(2*time.Minute, nil, nil)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("the catalog host answered %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > max {
		return nil, fmt.Errorf("%s exceeds the %d-byte bound", url, max)
	}
	return body, nil
}

// .
// .
// .
// .
func (a *App) runCatalogRefresh(ctx context.Context) {
	first := time.NewTimer(15 * time.Second)
	defer first.Stop()
	tick := time.NewTicker(catalogRefreshEvery)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-first.C:
		case <-tick.C:
		case <-a.catalogPoke:
		}
		rctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
		if err := a.refreshCatalog(rctx); err == nil {
			a.dashboard.BroadcastStatus()
		}
		cancel()
	}
}

// .
// .
func (a *App) pokeCatalog() {
	if a.catalogPoke == nil {
		return
	}
	select {
	case a.catalogPoke <- struct{}{}:
	default:
	}
}

// .
// .
func (a *App) effectiveCatalogURL() string {
	if u := strings.TrimSpace(a.configSnapshot().Plugins.CatalogURL); u != "" {
		return u
	}
	return pluginhost.DefaultCatalogURL
}

// .
func (a *App) catalogState() (url, fetchedAt, refusal string) {
	url = a.effectiveCatalogURL()
	a.catalogMu.RLock()
	fetchedAt, refusal = a.catalogAt, a.catalogErr
	a.catalogMu.RUnlock()
	return
}

// .
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

// .
func (a *App) catalogUpdateCount() int { return countUpdates(a.catalogViews()) }

// .
// .
// .
func (a *App) catalogViews() []dashboard.CatalogEntryView {
	cat := a.Catalog()
	if cat == nil {
		return nil
	}
	a.pluginMu.Lock()
	ver := make(map[string]string, len(a.plugins))
	for _, p := range a.plugins {
		ver[p.ID] = p.Version
	}
	a.pluginMu.Unlock()
	return catalogViewsFor(cat, ver, a.pendingSummaries())
}

// .
// .
// .
func catalogViewsFor(cat *pluginhost.Catalog, installed map[string]string, pending map[string]pendingMark) []dashboard.CatalogEntryView {
	out := make([]dashboard.CatalogEntryView, 0, len(cat.Plugins))
	for _, e := range cat.Plugins {
		_, pkg, _ := cat.Select(e.ID)
		v := dashboard.CatalogEntryView{ID: e.ID, Version: e.Version, Tier: e.Tier, Summary: e.Summary, Available: pkg != nil,
			Description: e.Description, Publisher: e.Publisher, Homepage: e.Homepage, License: e.License,
			Title: e.Title, Category: e.Category, Keywords: e.Keywords, Updated: e.Updated}
		if pkg != nil {
			v.Size = pkg.Size
		}
		if iv, ok := installed[e.ID]; ok {
			v.Installed = true
			v.InstalledVersion = iv
			v.UpdateAvailable = pkg != nil && pluginhost.NewerVersion(e.Version, iv)
		}
		// .
		// .
		// .
		if pm, ok := pending[e.ID]; ok {
			v.Pending, v.PendingText = pm.phase, pm.text
		}
		// .
		// .
		// .
		if ok, why := e.SupportedBy(version.Authored()); !ok {
			v.Available, v.Requires = false, why
		}
		out = append(out, v)
	}
	return out
}

// .
func (a *App) Catalog() *pluginhost.Catalog {
	a.catalogMu.RLock()
	defer a.catalogMu.RUnlock()
	return a.catalog
}

// .
// .
// .
// .
func (a *App) InstallFromCatalog(ctx context.Context, id string) error {
	cat := a.Catalog()
	if cat == nil {
		return fmt.Errorf("plugins: no catalog is configured or loaded")
	}
	entry, pkg, err := cat.Select(id)
	if err != nil {
		return err
	}
	dir := filepath.Join("plugins", id)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".download-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	fetch := a.pkgFetch
	if fetch == nil {
		fetch = a.fetchPackage
	}
	h := sha256.New()
	n, ferr := fetch(ctx, pkg.URL, io.MultiWriter(tmp, h))
	cerr := tmp.Close()
	if ferr != nil {
		os.Remove(tmpName)
		return fmt.Errorf("plugins: downloading %s: %w", id, ferr)
	}
	if cerr != nil {
		os.Remove(tmpName)
		return cerr
	}
	if n > maxCatalogPackageBytes {
		os.Remove(tmpName)
		return fmt.Errorf("plugins: %s exceeds the %d-byte install ceiling", id, maxCatalogPackageBytes)
	}
	if pkg.Size != 0 && n != pkg.Size {
		os.Remove(tmpName)
		return fmt.Errorf("plugins: %s downloaded %d bytes, the catalog declares %d — not installed", id, n, pkg.Size)
	}
	if got := "sha256:" + hex.EncodeToString(h.Sum(nil)); got != pkg.SHA256 {
		os.Remove(tmpName)
		return fmt.Errorf("plugins: %s failed its catalog hash (%s, catalog declares %s) — not installed", id, got, pkg.SHA256)
	}
	final := filepath.Join(dir, id+"-"+entry.Version+".aiiospkg")
	if err := os.Rename(tmpName, final); err != nil {
		os.Remove(tmpName)
		return err
	}
	// .
	// .
	if others, _ := filepath.Glob(filepath.Join(dir, "*.aiiospkg")); len(others) > 1 {
		for _, o := range others {
			if o != final {
				os.Remove(o)
			}
		}
	}
	a.pokePluginSweep()
	log.Printf("plugins: installed %s %s from the catalog (%s); the sweep will verify and activate it", id, entry.Version, pkg.URL)
	return nil
}

// .
// .
func (a *App) UninstallPlugin(id string) error {
	if id == "" || strings.ContainsAny(id, "/\\") || strings.Contains(id, "..") {
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
	log.Printf("plugins: uninstalled %s; the sweep will deactivate it", id)
	return nil
}

// .
// .
// .
func (a *App) fetchPackage(ctx context.Context, url string, w io.Writer) (int64, error) {
	if err := tools.FetchGuard(ctx, url); err != nil {
		return 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("User-Agent", "AII-OS/1.0 (plugin acquisition)")
	client := tools.GuardedClient(30*time.Minute, nil, nil)
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
