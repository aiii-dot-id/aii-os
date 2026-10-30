package app

import (
	"cmp"
	"context"
	"encoding/json"
	"github.com/aiii-dot-id/aii-os/internal/firewall"
	"io"
	"maps"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultCatalogueMaxBytes = 256 << 10

	defaultCapabilityTimeout = 8 * time.Second

	defaultCapabilityKeep = 6 * time.Hour
)

var reModalitySlug = regexp.MustCompile(`^[A-Za-z0-9._~-]+/[A-Za-z0-9._~:-]+$`)

type modelModalities struct {
	in    []string
	out   []string
	tools *bool

	ttl time.Duration
}

func (m modelModalities) known() bool { return len(m.in) > 0 || len(m.out) > 0 || m.tools != nil }

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
	modalityMemoMax = 128
)

func (m *modalityMemo) get(slug string) (modelModalities, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.by[slug]
	if !ok || time.Since(e.at) > cmp.Or(e.mods.ttl, defaultCapabilityKeep) {
		return modelModalities{}, false
	}
	return e.mods, true
}

func (m *modalityMemo) last(slug string) modelModalities {
	mods, _ := m.lastAt(slug)
	return mods
}

func (m *modalityMemo) lastAt(slug string) (modelModalities, time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.by[slug].mods, m.by[slug].at
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
	_, kept := a.modelListBounds()
	a.providers.runtime.mu.RLock()
	prior, listed := a.providers.runtime.provStatus[entry.Name]
	listed = listed && prior.state == probeOK && prior.key == probeKey(entry) && time.Since(prior.checkedAt) < kept
	a.providers.runtime.mu.RUnlock()
	list := entry.CapabilityList
	ask := validProviderURL(entry.URL) && (list == nil && !listed || list != nil && *list != "")
	if ask {

		bound := reg.capabilityLookup().timeout()
		if deadline, ok := ctx.Deadline(); ok {
			bound = min(bound, time.Until(deadline)/2)
		}
		nativeCtx, cancel := context.WithTimeout(ctx, bound)
		var models []string
		var meta map[string]modelMeta
		var err error
		if list == nil {
			models, meta, err = discover(a, nativeCtx, entry, entry.APIKey)
		} else {
			_, meta, err = a.discoverMetaAt(nativeCtx, entry, entry.APIKey, *list, model)
		}
		answered := err == nil && nativeCtx.Err() == nil
		cancel()
		switch {
		case answered && list == nil:
			a.providers.runtime.mu.Lock()
			if a.providers.runtime.provStatus == nil {
				a.providers.runtime.provStatus = make(map[string]providerProbe)
			}
			a.providers.runtime.provStatus[entry.Name] = retainNativeCapabilities(providerProbe{
				state: probeOK, models: models, meta: meta, key: probeKey(entry), checkedAt: time.Now(),
			}, a.providers.runtime.provStatus[entry.Name])
			a.providers.runtime.mu.Unlock()
		case answered:
			a.noteListedCapabilities(entry, meta)
		}
	}
	if row, ok := a.discoveredCapabilities(entry, model); ok && row.complete() {
		return
	}
	a.fetchModelModalities(ctx, modelModalitiesURL(entry, model, catalogueFor(entry)), guard, reg.capabilityLookup())
}

func (a *App) noteListedCapabilities(entry providerEntry, meta map[string]modelMeta) {
	a.providers.runtime.mu.Lock()
	defer a.providers.runtime.mu.Unlock()
	if a.providers.runtime.provStatus == nil {
		a.providers.runtime.provStatus = make(map[string]providerProbe)
	}
	st := a.providers.runtime.provStatus[entry.Name]
	if st.key != probeKey(entry) {

		st = providerProbe{state: st.state, reason: st.reason, models: st.models, key: probeKey(entry)}
	}
	merged := make(map[string]modelMeta, len(st.meta)+len(meta))
	maps.Copy(merged, st.meta)
	for name, said := range meta {
		row := merged[name]
		row.Input, row.Output = keepUnanswered(said.Input, row.Input), keepUnanswered(said.Output, row.Output)
		if said.Tools != nil {
			row.Tools = said.Tools
		}
		merged[name] = row
	}
	st.meta = merged
	a.providers.runtime.provStatus[entry.Name] = st
}

func (m modelMeta) complete() bool {
	for _, k := range inputKinds {
		if _, ok := m.Input[k]; !ok {
			return false
		}
	}
	for _, k := range outputKinds {
		if _, ok := m.Output[k]; !ok {
			return false
		}
	}
	return m.Tools != nil
}

func (a *App) startSelectedModalities(entry providerEntry, model string) {
	reg, err := a.loadProviders()
	if err != nil {
		return
	}
	guard := modalityGuard
	discover := selectedModelDiscovery
	a.runBackground(func() {
		ctx, cancel := context.WithTimeout(a.lifetime(), 2*reg.capabilityLookup().timeout())
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
	if mods.tools == nil {
		mods.tools = m.by[slug].mods.tools
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

func (a *App) fetchModelModalities(ctx context.Context, url string, guard func(context.Context, string) error, lookup capabilityLookup) modelModalities {
	if url == "" {
		return modelModalities{}
	}
	if mods, ok := a.modalities.get(url); ok {
		return mods
	}
	mods := lookupModalities(ctx, url, guard, lookup)
	a.modalities.put(url, mods)
	return mods
}

func lookupModalities(ctx context.Context, url string, guard func(context.Context, string) error, lookup capabilityLookup) modelModalities {
	if err := guard(ctx, url); err != nil {
		return modelModalities{}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return modelModalities{}
	}
	req.Header.Set("User-Agent", "AII-OS/1.0 (model capability)")
	resp, err := firewall.GuardedClient(lookup.timeout(), guard, nil).Do(req)
	if err != nil {
		return modelModalities{}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return modelModalities{}
	}
	limit := lookup.catalogueMaxBytes()
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil || int64(len(body)) > limit {
		return modelModalities{}
	}
	var out struct {
		Data struct {
			Architecture struct {
				InputModalities  []string `json:"input_modalities"`
				OutputModalities []string `json:"output_modalities"`
			} `json:"architecture"`
			Endpoints []struct {
				SupportedParameters []string `json:"supported_parameters"`
			} `json:"endpoints"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return modelModalities{}
	}
	mods := modelModalities{
		in:  out.Data.Architecture.InputModalities,
		out: out.Data.Architecture.OutputModalities,
		ttl: cmp.Or(maxAge(resp.Header.Get("Cache-Control")), lookup.keep()),
	}

	if len(out.Data.Endpoints) > 0 {
		takes := false
		for _, e := range out.Data.Endpoints {
			takes = takes || slices.Contains(e.SupportedParameters, "tools")
		}
		mods.tools = &takes
	}
	return mods
}

func maxAge(header string) time.Duration {
	for _, part := range strings.Split(header, ",") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(part), "max-age="); ok {
			if secs, err := strconv.Atoi(v); err == nil && secs > 0 {
				return time.Duration(secs) * time.Second
			}
		}
	}
	return 0
}

const (
	capFromOperator  = "operator"
	capFromMeasured  = "measured"
	capFromProvider  = "provider"
	capFromCatalogue = "catalogue"
	capFromShipped   = "shipped"
)

type capAnswer struct {
	state capState
	from  string
	at    time.Time
}

type capFigure struct {
	value int
	from  string
	at    time.Time
}

type modelCaps struct {
	input, output      map[string]capAnswer
	tools              capAnswer
	windows, outLimits []capFigure
	source, asOf       string
}

func said(figures []capFigure, from string) int {
	for _, f := range figures {
		if f.from == from {
			return f.value
		}
	}
	return 0
}

func figuresOf(figures ...capFigure) []capFigure {
	return slices.DeleteFunc(figures, func(f capFigure) bool { return f.value <= 0 })
}

var catalogueKinds = map[string]string{"image": "image", "pdf": "file", "audio": "audio", "video": "video", "text": "text"}

func (a *App) modelCapabilitiesFor(reg *providerRegistry, entry providerEntry, model string) modelCaps {
	caps := modelCaps{input: map[string]capAnswer{}, output: map[string]capAnswer{}}
	if model == "" || reg == nil {
		return caps
	}
	own := reg.ModelCapabilities[model]
	shipped := embeddedRegistry().ModelCapabilities[model]
	caps.source, caps.asOf = shipped.Source, shipped.AsOf
	native, _ := a.discoveredCapabilities(entry, model)
	listed, listedAt := a.modalities.lastAt(modelModalitiesURL(entry, model, catalogueFor(entry)))
	if !listed.known() {
		listedAt = time.Time{}
	}
	for _, kind := range inputKinds {
		caps.input[kind] = firstAnswer(
			capAnswer{state: capStateOf(own.takes(kind)), from: capFromOperator},
			capAnswer{state: capStateOf(saidOf(native.Input, kind)), from: capFromProvider},
			capAnswer{state: capStateOf(catalogueSays(listed.in, kind)), from: capFromCatalogue, at: listedAt},
			capAnswer{state: capStateOf(shipped.takes(kind)), from: capFromShipped},
		)
	}
	for _, kind := range outputKinds {
		caps.output[kind] = firstAnswer(
			capAnswer{state: capStateOf(own.gives(kind)), from: capFromOperator},
			capAnswer{state: capStateOf(saidOf(native.Output, kind)), from: capFromProvider},
			capAnswer{state: capStateOf(catalogueSays(listed.out, kind)), from: capFromCatalogue, at: listedAt},
			capAnswer{state: capStateOf(shipped.gives(kind)), from: capFromShipped},
		)
	}
	caps.windows = figuresOf(
		capFigure{value: own.ContextLength, from: capFromOperator},
		capFigure{value: native.Context, from: capFromProvider},
		capFigure{value: shipped.ContextLength, from: capFromShipped},
	)
	caps.outLimits = figuresOf(
		capFigure{value: own.MaxOutputTokens, from: capFromOperator},
		capFigure{value: native.MaxOut, from: capFromProvider},
		capFigure{value: shipped.MaxOutputTokens, from: capFromShipped},
	)
	measured := a.substrateCapabilityFor(entry.Name, model)
	caps.tools = firstAnswer(
		capAnswer{state: capStateOf(own.Tools), from: capFromOperator},
		capAnswer{state: measured.toolCalls, from: capFromMeasured, at: measured.checkedAt},
		capAnswer{state: capStateOf(native.Tools), from: capFromProvider},
		capAnswer{state: capStateOf(listed.tools), from: capFromCatalogue, at: listedAt},
		capAnswer{state: capStateOf(shipped.Tools), from: capFromShipped},
	)
	return caps
}

func firstAnswer(answers ...capAnswer) capAnswer {
	for _, a := range answers {
		if a.state != capUnknown {
			return a
		}
	}
	return capAnswer{}
}

func capStateOf(v *bool) capState {
	switch {
	case v == nil:
		return capUnknown
	case *v:
		return capYes
	}
	return capNo
}

func saidOf(kinds map[string]bool, kind string) *bool {
	if v, ok := kinds[kind]; ok {
		return &v
	}
	return nil
}

func catalogueSays(list []string, kind string) *bool {
	if list == nil {
		return nil
	}
	v := slices.Contains(list, catalogueKinds[kind])
	return &v
}
