package app

import (
	"context"
	"encoding/json"
	"github.com/aiii-dot-id/aii-os/internal/firewall"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	modalityMaxBytes = 256 << 10
	modalityTimeout  = 8 * time.Second
)

var reModalitySlug = regexp.MustCompile(`^[A-Za-z0-9._~-]+/[A-Za-z0-9._~:-]+$`)

type modelModalities struct {
	in  []string
	out []string
}

func (m modelModalities) known() bool { return len(m.in) > 0 || len(m.out) > 0 }

func modalitySlug(entry providerEntry, model string) string {
	model = strings.TrimSpace(model)
	if model == "" || entry.Local {
		return ""
	}
	slug := model
	if !strings.Contains(model, "/") {
		if entry.CatalogueAuthor == "" {
			return ""
		}
		slug = entry.CatalogueAuthor + "/" + model
	}
	if !reModalitySlug.MatchString(slug) {
		return ""
	}
	return slug
}

type modalityMemo struct {
	mu sync.Mutex
	by map[string]modalityMemoEntry
}

type modalityMemoEntry struct {
	mods modelModalities
	at   time.Time
}

const (
	modalityMemoTTL = 6 * time.Hour

	modalityMemoMax = 128
)

func (m *modalityMemo) get(slug string) (modelModalities, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.by[slug]
	if !ok || time.Since(e.at) > modalityMemoTTL {
		return modelModalities{}, false
	}
	return e.mods, true
}

func (m *modalityMemo) last(slug string) modelModalities {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.by[slug].mods
}

func (a *App) refreshSelectedModalities(ctx context.Context, entry providerEntry, model string, reg *providerRegistry, guard func(context.Context, string) error, discover func(*App, context.Context, providerEntry, string) ([]string, map[string]modelMeta, error)) {
	if a.dashboard != nil {

		defer a.dashboard.BroadcastConfig()
	}

	if reg != nil {
		for _, e := range reg.Providers {
			if e.Name == entry.Name {
				entry = e
				break
			}
		}
	}
	a.providers.runtime.mu.RLock()
	prior, listed := a.providers.runtime.provStatus[entry.Name]
	listed = listed && prior.state == probeOK && prior.key == probeKey(entry) && time.Since(prior.checkedAt) < providerStatusTTL
	a.providers.runtime.mu.RUnlock()
	if !listed && validProviderURL(entry.URL) {

		bound := modalityTimeout
		if deadline, ok := ctx.Deadline(); ok {
			bound = min(bound, time.Until(deadline)/2)
		}
		nativeCtx, cancel := context.WithTimeout(ctx, bound)
		models, meta, err := discover(a, nativeCtx, entry, entry.APIKey)
		answered := err == nil && nativeCtx.Err() == nil
		cancel()
		if answered {
			a.providers.runtime.mu.Lock()
			if a.providers.runtime.provStatus == nil {
				a.providers.runtime.provStatus = make(map[string]providerProbe)
			}
			a.providers.runtime.provStatus[entry.Name] = retainNativeImages(providerProbe{
				state: probeOK, models: models, meta: meta, key: probeKey(entry), checkedAt: time.Now(),
			}, a.providers.runtime.provStatus[entry.Name])
			a.providers.runtime.mu.Unlock()
		}
	}
	if a.discoveredImageSupport(entry, model) != nil {
		return
	}
	a.fetchModelModalities(ctx, modelModalitiesURL(entry, model, reg.modelCatalogueURL()), guard)
}

func (a *App) startSelectedModalities(entry providerEntry, model string) {
	reg, err := a.loadProviders()
	if err != nil {
		return
	}
	guard := modalityGuard
	discover := selectedModelDiscovery
	a.runBackground(func() {
		ctx, cancel := context.WithTimeout(a.lifetime(), 2*modalityTimeout)
		defer cancel()
		a.refreshSelectedModalities(ctx, entry, model, reg, guard, discover)
	})
}

func (m *modalityMemo) put(slug string, mods modelModalities) {
	if !mods.known() {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.by == nil {
		m.by = make(map[string]modalityMemoEntry, 8)
	}
	if _, exists := m.by[slug]; !exists && len(m.by) >= modalityMemoMax {
		var oldest string
		for key, entry := range m.by {
			if oldest == "" || entry.at.Before(m.by[oldest].at) {
				oldest = key
			}
		}
		delete(m.by, oldest)
	}

	if mods.in == nil {
		mods.in = m.by[slug].mods.in
	}
	if mods.out == nil {
		mods.out = m.by[slug].mods.out
	}
	m.by[slug] = modalityMemoEntry{mods: mods, at: time.Now()}
}

func modelModalitiesURL(entry providerEntry, model, baseURL string) string {
	slug := modalitySlug(entry, model)
	if slug == "" || baseURL == "" {
		return ""
	}
	return strings.TrimRight(baseURL, "/") + "/" + slug + "/endpoints"
}

func (a *App) fetchModelModalities(ctx context.Context, url string, guard func(context.Context, string) error) modelModalities {
	if url == "" {
		return modelModalities{}
	}
	if mods, ok := a.modalities.get(url); ok {
		return mods
	}
	mods := lookupModalities(ctx, url, guard)
	a.modalities.put(url, mods)
	return mods
}

func lookupModalities(ctx context.Context, url string, guard func(context.Context, string) error) modelModalities {
	if err := guard(ctx, url); err != nil {
		return modelModalities{}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return modelModalities{}
	}
	req.Header.Set("User-Agent", "AII-OS/1.0 (model capability)")
	resp, err := firewall.GuardedClient(modalityTimeout, guard, nil).Do(req)
	if err != nil {
		return modelModalities{}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return modelModalities{}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, modalityMaxBytes+1))
	if err != nil || len(body) > modalityMaxBytes {
		return modelModalities{}
	}
	var out struct {
		Data struct {
			Architecture struct {
				InputModalities  []string `json:"input_modalities"`
				OutputModalities []string `json:"output_modalities"`
			} `json:"architecture"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return modelModalities{}
	}
	return modelModalities{
		in:  out.Data.Architecture.InputModalities,
		out: out.Data.Architecture.OutputModalities,
	}
}
