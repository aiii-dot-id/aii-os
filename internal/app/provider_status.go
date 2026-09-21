// .
// .
// .
// .
package app

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/oauth"
)

// .
// .
func (a *App) setProviderInfo(in dashboard.ProviderInfo) error {
	// .
	// .
	// .
	// .
	// .
	var chat *bool
	if in.Chat {
		yes := true
		chat = &yes
	}
	return a.setProvider(providerEntry{
		Name: in.Name, APIType: in.APIType, URL: in.Endpoint, Chat: chat,
		APIKey: in.APIKey, APIKeyEnv: in.APIKeyEnv, Credential: in.Credential,
		DefaultModel: in.DefaultModel, SubscribeURL: in.SubscribeURL,
		ContextLength: in.ContextLength, MaxOutputTokens: in.MaxOutputTokens,
		// .
		// .
		// .
		// .
		// .
		ReasoningEffort: in.ReasoningEffort, ThinkingBudget: in.ThinkingBudget, ThinkingMode: in.ThinkingMode, ThinkingDisplay: in.ThinkingDisplay,
		Temperature: in.Temperature, TopP: in.TopP, Extra: in.Extra, Cache: in.Cache,
		Default: in.Default, Models: in.ConfiguredModels,
	}, in.APIKey == "" && in.HasKey)
}

// .
// .
// .
// .
// .
// .
// .

const providerStatusTTL = 60 * time.Second

type providerProbe struct {
	state     string
	reason    string
	models    []string
	meta      map[string]modelMeta
	checkedAt time.Time
	key       string
}

func probeKey(e providerEntry) string {
	// .
	// .
	// .
	// .
	// .
	// .
	e.EffortLevels = nil
	e.CatalogueAuthor = ""
	// .
	// .
	e.Speech = nil
	data, _ := json.Marshal(e)
	hash := sha256.Sum256(data)
	return string(hash[:])
}

// .
// .
// .
func (a *App) probeProviders(reg *providerRegistry) map[string]providerProbe {
	now := time.Now()
	a.provMu.Lock()
	if a.provStatus == nil {
		a.provStatus = make(map[string]providerProbe)
	}
	var stale []providerEntry
	out := make(map[string]providerProbe, len(reg.Providers))
	for _, e := range reg.Providers {
		if !chatProvider(e) {
			continue
		}
		st, ok := a.provStatus[e.Name]
		if ok && st.key == probeKey(e) && now.Sub(st.checkedAt) < providerStatusTTL {
			out[e.Name] = st
			continue
		}
		stale = append(stale, e)
	}
	a.provMu.Unlock()

	if len(stale) > 0 {
		type res struct {
			name string
			st   providerProbe
		}
		ch := make(chan res, len(stale))
		for _, e := range stale {
			go func(e providerEntry) {
				ch <- res{e.Name, a.probeOne(e)}
			}(e)
		}
		results := make([]res, 0, len(stale))
		for range stale {
			results = append(results, <-ch)
		}
		a.provMu.Lock()
		for _, r := range results {
			st := r.st
			prev := a.provStatus[r.name]
			// .
			// .
			// .
			// .
			if st.state != "ok" && len(prev.models) > 0 {
				st.models, st.meta = prev.models, prev.meta
			}
			if st.state != prev.state || st.reason != prev.reason {
				logsink.Info("providers.decision", "%s — %s%s (%d model(s) listed)", r.name, st.state, reasonSuffix(st.reason), len(st.models))
			}
			a.provStatus[r.name] = st
			out[r.name] = st
		}
		a.provMu.Unlock()
	}
	return out
}

func (a *App) probeOne(e providerEntry) providerProbe {
	st := providerProbe{checkedAt: time.Now(), key: probeKey(e)}
	if !validProviderURL(e.URL) {
		st.state = "invalid_url"
		return st
	}
	ctx := a.bgCtx
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	// .
	// .
	// .
	models, meta, err := a.discoverMetaForEntry(ctx, e, e.APIKey)
	switch {
	case err == nil:
		st.state = "ok"
		st.models, st.meta = models, meta
	case err == errAuthRequired:
		st.state = "auth_required"
	case e.Credential != "" && classifyCredentialErr(err) != "":
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		st.state = classifyCredentialErr(err)
		st.reason = err.Error()
	default:
		st.state = "unreachable"
		st.reason = err.Error()
	}
	return st
}

// .
// .
// .
// .
func classifyCredentialErr(err error) string {
	switch {
	case errors.Is(err, oauth.ErrOwnerRefreshRequired), errors.Is(err, oauth.ErrGrantInvalid):
		// .
		// .
		// .
		return "credential_expired"
	case errors.Is(err, errCredentialUnavailable):
		return "no_credential"
	}
	return ""
}

// .
// .
func wrapUnavailable(err error) error {
	return fmt.Errorf("%w: %w", errCredentialUnavailable, err)
}

// .
// .
// .
func validProviderURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

func validEnvName(name string) bool {
	for i, r := range name {
		if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || r == '_' || (i > 0 && r >= '0' && r <= '9') {
			continue
		}
		return false
	}
	return name != ""
}

// .
// .
func (a *App) providerDirectory() dashboard.ProviderDirectory {
	dir := dashboard.ProviderDirectory{SkipSignInWithValidToken: a.configSnapshot().Dashboard.skipSignInWithValidToken()}
	reg, err := a.loadProviders()
	if err != nil {
		logsink.Warn("providers.error", "%v", err)
		return dir
	}
	dir.Providers = a.providerInfos(reg)
	dir.Broken = brokenProviderInfo(reg)
	return dir
}

// .
// .
// .
func (a *App) providerDirectoryLive() []dashboard.ProviderInfo {
	reg, err := a.loadProviders()
	if err != nil {
		logsink.Warn("providers.error", "%v", err)
		return nil
	}
	return a.providerInfos(reg)
}

func (a *App) providerInfos(reg *providerRegistry) []dashboard.ProviderInfo {
	probes := a.probeProviders(reg)
	out := make([]dashboard.ProviderInfo, 0, len(reg.Providers))
	for _, e := range reg.Providers {
		pr := probes[e.Name]
		// .
		// .
		// .
		models := mergeModels(e.Models, seedModelsFor(e.Name), pr.models)
		if e.DefaultModel != "" {
			models = mergeModels([]string{e.DefaultModel}, models)
		}
		dialect := entryDialect(e)
		explicit := false
		var retentions []string
		if cap, ok := reg.effective().capabilityFor(effortModelFor(e)); ok {
			explicit = cap.cacheBreakpoints()
			retentions = cap.CacheRetentions
		}
		cache := e.Cache
		if cache == nil {
			if effective, err := llm.ResolveCachePolicy(nil, e.Extra, dialect, explicit, retentions); err == nil && effective != (llm.CachePolicy{}) {
				cache = &effective
			}
		}
		out = append(out, dashboard.ProviderInfo{
			Chat: chatProvider(e), Speech: speechInfo(e),
			Name: e.Name, APIType: e.APIType, Endpoint: e.URL,
			CacheModes: llm.CacheModes(dialect, explicit), CacheTTLs: llm.CacheTTLs(dialect, explicit, retentions), CacheDiagnostics: dialect == llm.DialectAnthropic, CacheKeySupported: dialect != llm.DialectAnthropic,
			HasKey: e.APIKey != "", APIKeyEnv: e.APIKeyEnv, Credential: e.Credential,
			CredentialInfo: a.credentialInfo(e),
			CanSignIn:      canSignIn(e),
			SignIn:         a.signInView("provider:" + e.Name),
			DefaultModel:   e.DefaultModel, SubscribeURL: e.SubscribeURL,
			ContextLength: e.ContextLength, MaxOutputTokens: e.MaxOutputTokens,
			// .
			// .
			// .
			// .
			ReasoningEffort: e.ReasoningEffort,
			EffortLevels:    effortLevelsFor(reg.effective(), e, effortModelFor(e)),
			EffortModel:     effortModelFor(e),
			SummaryField:    summaryFieldFor(e), SummaryLevels: llm.ReasoningSummaryLevels(entryDialect(e)),
			ThinkingBudget: e.ThinkingBudget, ThinkingMode: e.ThinkingMode, ThinkingDisplay: e.ThinkingDisplay,
			Temperature: e.Temperature, TopP: e.TopP, Extra: e.Extra, Cache: cache,
			Default: e.Default, Models: models, ConfiguredModels: e.Models, Status: pr.state, StatusReason: pr.reason,
		})
	}
	markPreselect(out)
	return out
}

// .
type oauthAdapter struct{ s *oauth.Source }

func (a oauthAdapter) Credential(ctx context.Context) (llm.Credential, error) {
	c, err := a.s.Credential(ctx)
	if err != nil {
		return llm.Credential{}, err
	}
	return llm.Credential{Token: c.Token, Headers: c.Headers, Gen: c.Gen}, nil
}

func (a oauthAdapter) Stale(ctx context.Context, gen uint64) error { return a.s.Stale(ctx, gen) }

// .
// .
// .
// .
// .
// .
func (a *App) entryTransport(e providerEntry, apiKey string) (llm.ClientConfig, error) {
	cc := llm.ClientConfig{Endpoint: e.URL, Provider: e.APIType, APIKey: apiKey}
	if e.Credential == "" {
		return cc, nil
	}
	src, cerr := a.credentialSource(e.Credential, e.CredentialOptions, e.signIn)
	if cerr != nil {
		return llm.ClientConfig{}, fmt.Errorf("provider %q credential: %w", e.Name, cerr)
	}
	if b := src.BaseURL(); b != "" {
		cc.Endpoint = b
	}
	if d := src.Dialect(); d != "" {
		cc.Provider = d
	}
	cc.APIKey = ""
	cc.Credential = oauthAdapter{src}
	cc.AnthropicOAuthBillingText = src.BillingText()
	return cc, nil
}

// .
// .
// .
func (a *App) clientConfigForEntry(eff *effectiveCaps, e providerEntry, cfg LLMConfig, model, apiKey string) (llm.ClientConfig, error) {
	if cap, ok := eff.capabilityFor(model); ok && cap.MaxOutputTokens < 0 {
		return llm.ClientConfig{}, fmt.Errorf("model %q has a negative output maximum", model)
	}
	e = limitModelOutput(e, model, eff)
	cc, err := a.entryTransport(e, providerAPIKey(e, apiKey, cfg.APIKeyEnv))
	if err != nil {
		return llm.ClientConfig{}, err
	}
	cc.Model = model
	cc.ThinkingBudget = e.ThinkingBudget
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	cc.ThinkingMode = thinkingShapeFor(eff, e, model)
	cc.ThinkingDisplay = e.ThinkingDisplay
	cc.ReasoningEffort = e.ReasoningEffort
	// .
	// .
	// .
	// .
	// .
	// .
	cc.EffortLevels = effortLevelsFor(eff, e, model)
	cc.MaxOutputTokens = e.MaxOutputTokens
	cc.Temperature = e.Temperature
	cc.TopP = e.TopP
	cc.Extra = e.Extra
	cc.Cache = e.Cache
	if capability, ok := eff.capabilityFor(model); ok {
		cc.ExplicitCache = capability.cacheBreakpoints() && cc.Provider != "chatgpt"
		cc.MaxCompletionTokens = capability.modernOutputLimit() && cc.Provider != "chatgpt"
		cc.CacheRetentions = capability.CacheRetentions
	}
	if _, err := llm.ResolveCachePolicy(cc.Cache, cc.Extra, llm.DialectFor(cc.Provider), cc.ExplicitCache, cc.CacheRetentions); err != nil {
		return llm.ClientConfig{}, err
	}
	cc.TimeoutSeconds = cfg.TimeoutSeconds
	cc.NoStream = cfg.Stream != nil && !*cfg.Stream
	cc.Retries = cfg.Retries
	cc.RetryBackoffMS = cfg.RetryBackoffMS
	return cc, nil
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
func (a *App) birthEntry(name, endpoint, model string) (providerEntry, *effectiveCaps, error) {
	e := providerEntry{Name: name, URL: endpoint, DefaultModel: model}
	reg, err := a.loadProviders()
	if err != nil {
		return providerEntry{}, nil, err
	}
	if name == "" {
		return e, reg.effective(), nil
	}
	for _, stored := range reg.Providers {
		if stored.Name != name {
			continue
		}
		e = stored
		if endpoint != "" {
			e.URL = endpoint
		}
		if model != "" {
			e.DefaultModel = model
		}
		return e, reg.effective(), nil
	}
	return e, reg.effective(), nil
}

// .
// .
func apiVersionInPath(base string) bool {
	u, err := url.Parse(base)
	if err != nil {
		return false
	}
	for _, seg := range strings.Split(u.Path, "/") {
		if len(seg) >= 2 && (seg[0] == 'v' || seg[0] == 'V') && seg[1] >= '0' && seg[1] <= '9' {
			return true
		}
	}
	return false
}

// .
// .
// .
// .
func (a *App) discoveredMeta(provider, model string) (m modelMeta, found bool, listed int) {
	a.provMu.Lock()
	defer a.provMu.Unlock()
	pr, ok := a.provStatus[provider]
	if !ok || pr.meta == nil {
		return modelMeta{}, false, len(pr.models)
	}
	m, found = pr.meta[model]
	return m, found, len(pr.models)
}

// .
// .
// .
// .
// .
// .
// .
func markPreselect(out []dashboard.ProviderInfo) {
	for i := range out {
		if out[i].Credential != "" && out[i].Status == "ok" {
			out[i].Preselect = true
			out[i].PreselectWhy = "a working " + out[i].Credential + " credential is on this machine — no key needed"
			return
		}
	}
	for i := range out {
		if out[i].Default {
			out[i].Preselect = true
			return
		}
	}
}

// .
// .
// .
func (a *App) credentialInfo(e providerEntry) *dashboard.CredentialInfo {
	if e.Credential == "" {
		return nil
	}
	src, err := a.credentialSource(e.Credential, e.CredentialOptions, e.signIn)
	if err != nil {
		return &dashboard.CredentialInfo{Kind: e.Credential, Error: err.Error()}
	}
	i := src.Info()
	out := &dashboard.CredentialInfo{
		Kind: i.Kind, Plan: i.Plan, Tier: i.Tier, IsAPIKey: i.IsAPIKey, Path: i.Path, Error: i.Error,
	}
	if !i.ExpiresAt.IsZero() {
		// .
		// .
		// .
		// .
		// .
		usable := i.ExpiresAt.Add(-oauth.ExpirySkew)
		out.ExpiresAt = usable.UTC().Format(time.RFC3339)
		out.Expired = !time.Now().Before(usable)
	}
	return out
}

// .
// .
// .
// .
const credentialWarnWindow = 30 * time.Minute

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
func (a *App) credentialWarning() string {
	_, entry, err := a.resolveLLM()
	if err != nil || entry.Credential == "" {
		return ""
	}
	info := a.credentialInfo(entry)
	if info == nil || info.ExpiresAt == "" {
		return ""
	}
	usable, perr := time.Parse(time.RFC3339, info.ExpiresAt)
	if perr != nil {
		return ""
	}
	return credentialWarningFor(entry.Credential, usable, time.Now())
}

// .
// .
// .
func credentialWarningFor(kind string, usable, now time.Time) string {
	left := usable.Sub(now)
	if left > credentialWarnWindow {
		return ""
	}
	if left <= 0 {
		return fmt.Sprintf("the %s credential is no longer usable — refresh it with its own tool; the identity cannot think until you do", kind)
	}
	return fmt.Sprintf("the %s credential becomes unusable in %d min — refresh it with its own tool", kind, int(left.Minutes())+1)
}

// .
// .
// .
// .
func entryDialect(e providerEntry) llm.Dialect {
	if e.Credential != "" {
		if d := oauth.Dialect(e.Credential, e.CredentialOptions); d != "" {
			return llm.DialectFor(d)
		}
	}
	return llm.DialectFor(e.APIType)
}

// .
// .
func summaryFieldFor(e providerEntry) string {
	return llm.ReasoningSummaryField(entryDialect(e))
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
func effortModelFor(e providerEntry) string {
	if e.DefaultModel != "" {
		return e.DefaultModel
	}
	if len(e.Models) > 0 {
		return e.Models[0]
	}
	return ""
}

func reasonSuffix(r string) string {
	if r == "" {
		return ""
	}
	return ": " + r
}

var (
	seedModelsOnce sync.Once
	seedModels     map[string][]string
)

// .
// .
// .
// .
// .
func seedModelsFor(name string) []string {
	seedModelsOnce.Do(func() {
		seedModels = map[string][]string{}
		var reg providerRegistry
		if err := json.Unmarshal(embeddedProviders, &reg); err == nil {
			for _, e := range reg.Providers {
				seedModels[e.Name] = e.Models
			}
		}
	})
	return seedModels[name]
}

// .
// .
// .
// .
// .
// .
func (a *App) askWindowIfUndeclaredIn(reg *providerRegistry, llmCfg LLMConfig) {
	entry, err := selectProvider(llmCfg, reg)
	if err != nil || (entry.ContextLength > 0 && entry.MaxOutputTokens > 0) {
		return
	}
	a.probeProviders(&providerRegistry{Providers: []providerEntry{*entry}})
}

func (a *App) askWindowIfUndeclared(llmCfg LLMConfig) {
	if reg, err := a.loadProviders(); err == nil {
		a.askWindowIfUndeclaredIn(reg, llmCfg)
	}
}
