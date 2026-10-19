package app

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/oauth"
)

func (a *App) setProviderInfo(in dashboard.ProviderEdit) error {

	var chat *bool
	if in.Chat {
		yes := true
		chat = &yes
	}
	return a.setProviderFromCard(providerEntry{
		Name: in.Name, APIType: in.APIType, URL: in.Endpoint, Chat: chat,
		APIKey: in.APIKey, APIKeyEnv: in.APIKeyEnv, Credential: in.Credential,
		DefaultModel: in.DefaultModel, SubscribeURL: in.SubscribeURL,
		ContextLength: in.ContextLength, MaxOutputTokens: in.MaxOutputTokens,

		ReasoningEffort: in.ReasoningEffort, ThinkingBudget: in.ThinkingBudget, ThinkingMode: in.ThinkingMode, ThinkingDisplay: in.ThinkingDisplay,
		Temperature: in.Temperature, TopP: in.TopP, Extra: in.Extra, Cache: in.Cache,
		Default: in.Default, Models: in.ConfiguredModels,
		EmbeddingsModel: in.EmbeddingsModel,
	}, !in.ClearKey, !in.SetEmbeddingsModel)
}

const providerStatusTTL = 60 * time.Second

const (
	probeOK                = "ok"
	probeAuthRequired      = "auth_required"
	probeNoCredential      = "no_credential"
	probeCredentialExpired = "credential_expired"
	probeUnreachable       = "unreachable"
	probeInvalidURL        = "invalid_url"
)

type providerProbe struct {
	state     string
	reason    string
	models    []string
	meta      map[string]modelMeta
	checkedAt time.Time
	key       string
}

func retainNativeImages(fresh, previous providerProbe) providerProbe {
	if fresh.key != previous.key || fresh.state != probeOK {
		return fresh
	}
	for model, old := range previous.meta {
		row := fresh.meta[model]
		if old.Images == nil || row.Images != nil || !modelOffered(fresh.models, fresh.meta, model) {
			continue
		}
		if fresh.meta == nil {
			fresh.meta = make(map[string]modelMeta)
		}
		row.Images = old.Images
		fresh.meta[model] = row
	}
	return fresh
}

func probeKey(e providerEntry) string {

	e.EffortLevels = nil
	e.CatalogueAuthor = ""

	e.Speech = nil
	data, _ := json.Marshal(e)
	hash := sha256.Sum256(data)
	return string(hash[:])
}

func (a *App) probeProviders(reg *providerRegistry) map[string]providerProbe {
	now := time.Now()
	a.providers.runtime.mu.Lock()
	if a.providers.runtime.provStatus == nil {
		a.providers.runtime.provStatus = make(map[string]providerProbe)
	}
	var stale []providerEntry
	out := make(map[string]providerProbe, len(reg.Providers))
	for _, e := range reg.Providers {
		if !chatProvider(e) {
			continue
		}
		st, ok := a.providers.runtime.provStatus[e.Name]
		if ok && st.key == probeKey(e) && now.Sub(st.checkedAt) < providerStatusTTL {
			out[e.Name] = st
			continue
		}
		stale = append(stale, e)
	}
	a.providers.runtime.mu.Unlock()

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
		a.providers.runtime.mu.Lock()
		for _, r := range results {
			st := r.st
			prev := a.providers.runtime.provStatus[r.name]
			st = retainNativeImages(st, prev)

			if st.state != probeOK && len(prev.models) > 0 {
				st.models = prev.models
				if st.key == prev.key {
					st.meta = prev.meta
				}
			}
			if st.state != prev.state || st.reason != prev.reason {
				logsink.Info("providers.decision", "%s — %s%s (%d model(s) listed)", r.name, st.state, reasonSuffix(st.reason), len(st.models))
			}
			a.providers.runtime.provStatus[r.name] = st
			out[r.name] = st
		}
		a.providers.runtime.mu.Unlock()
	}
	return out
}

func (a *App) probeOne(e providerEntry) providerProbe {
	st := providerProbe{checkedAt: time.Now(), key: probeKey(e)}
	if !validProviderURL(e.URL) {
		st.state = probeInvalidURL
		return st
	}
	ctx, cancel := context.WithTimeout(a.lifetime(), 3*time.Second)
	defer cancel()

	models, meta, err := a.discoverMetaForEntry(ctx, e, e.APIKey)

	withheld, set := keyWithheldReason(e, a.llmBlockEnvFor(e))
	switch {
	case err == nil:
		st.state = probeOK
		st.models, st.meta = models, meta
		if set {
			st.reason = withheld
		}
	case errors.Is(err, errAuthRequired):

		st.state = probeAuthRequired
		st.reason = withheld
	case e.Credential != "" && classifyCredentialErr(err) != "":

		st.state = classifyCredentialErr(err)
		st.reason = err.Error()
	default:
		st.state = probeUnreachable
		st.reason = err.Error()
		if set {
			st.reason = withheld + " — " + err.Error()
		}
	}
	return st
}

func keyWithheldReason(e providerEntry, blockEnv string) (why string, set bool) {
	if e.APIKey != "" || e.APIKeyEnv != "" || e.Credential != "" || (blockEnv != "" && blockEnvIsFor(e, blockEnv)) {
		return "", false
	}
	env, withheld, home := lentKeyEnv(e)
	if env != "" || withheld == "" {
		return "", false
	}
	return fmt.Sprintf("no key was sent: %s is %s's own key variable and goes only to %s, and this entry is at %s. "+
		"To send a key here, enter it in API KEY, or name a variable for this entry (api_key_env) in providers.json",
		withheld, e.Name, home, addressOrigin(e.URL)), os.Getenv(withheld) != ""
}

func classifyCredentialErr(err error) string {
	switch {
	case errors.Is(err, oauth.ErrOwnerRefreshRequired), errors.Is(err, oauth.ErrGrantInvalid):

		return probeCredentialExpired
	case errors.Is(err, errCredentialUnavailable):
		return probeNoCredential
	}
	return ""
}

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

func (a *App) providerInfos(reg *providerRegistry) []dashboard.ProviderInfo {
	probes := a.probeProviders(reg)
	out := make([]dashboard.ProviderInfo, 0, len(reg.Providers))
	for _, e := range reg.Providers {
		pr := probes[e.Name]

		models := mergeDistinct(e.Models, seedModelsFor(e.Name), pr.models)
		if e.DefaultModel != "" {
			models = mergeDistinct([]string{e.DefaultModel}, models)
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
			Chat: chatProvider(e), Speech: speechInfo(e), Local: e.Local,
			Name: e.Name, APIType: e.APIType, Endpoint: e.URL, EmbeddingsModel: e.EmbeddingsModel,
			CacheModes: llm.CacheModes(dialect, explicit), CacheTTLs: llm.CacheTTLs(dialect, explicit, retentions), CacheDiagnostics: dialect == llm.DialectAnthropic, CacheKeySupported: dialect == llm.DialectOpenAI || dialect == llm.DialectResponses,
			HasKey: e.APIKey != "", APIKeyEnv: e.APIKeyEnv, Credential: e.Credential,
			CredentialInfo: a.credentialInfo(e),
			CanSignIn:      canSignIn(e),
			SignInUnmet:    signInUnmet(e),
			SignIn:         a.signInView("provider:" + e.Name),
			DefaultModel:   e.DefaultModel, SubscribeURL: e.SubscribeURL,
			ContextLength: e.ContextLength, MaxOutputTokens: e.MaxOutputTokens,

			ReasoningEffort: e.ReasoningEffort,
			EffortLevels:    effortLevelsFor(reg.effective(), e, effortModelFor(e)),
			EffortModel:     effortModelFor(e),
			SummaryField:    summaryFieldFor(e), SummaryLevels: llm.ReasoningSummaryLevels(entryDialect(e)),
			ThinkingBudget: e.ThinkingBudget, ThinkingMode: e.ThinkingMode, ThinkingDisplay: displayOnCard(e),
			Temperature: e.Temperature, TopP: e.TopP, Extra: e.Extra, Cache: cache,
			Default: e.Default, Models: models, ConfiguredModels: e.Models, Status: pr.state, StatusReason: pr.reason,
		})
	}
	markPreselect(out)
	return out
}

type oauthAdapter struct{ s *oauth.Source }

func (a oauthAdapter) Credential(ctx context.Context) (llm.Credential, error) {
	c, err := a.s.Credential(ctx)
	if err != nil {
		return llm.Credential{}, err
	}
	return llm.Credential{Token: c.Token, Headers: c.Headers, Gen: c.Gen}, nil
}

func (a oauthAdapter) Stale(ctx context.Context, gen uint64) error { return a.s.Stale(ctx, gen) }

func (a *App) entryTransport(e providerEntry, apiKey string) (llm.ClientConfig, error) {
	cc := llm.ClientConfig{Endpoint: e.URL, Provider: e.APIType, APIKey: apiKey, ToolRefusals: a.toolRefusals, Entry: e.Name, Sent: a.sentRequests}
	if e.Credential == "" {
		return cc, nil
	}
	if err := credentialAddressRefusal(e); err != nil {
		return llm.ClientConfig{}, err
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
	profile := e.ClaudeCode
	if profile == nil {
		for _, shipped := range embeddedRegistry().Providers {
			if shipped.Credential == e.Credential && shipped.ClaudeCode != nil {
				profile = shipped.ClaudeCode
				break
			}
		}
	}
	if profile != nil && profile.Enabled {
		resolved := *profile
		resolved.Version = e.CredentialOptions["client_version"]
		if err := resolved.Validate(); err != nil {
			return llm.ClientConfig{}, fmt.Errorf("provider %q wire profile: %w", e.Name, err)
		}
		cc.ClaudeCode = &resolved
	}

	return cc, nil
}

func (a *App) clientConfigForEntry(eff *effectiveCaps, e providerEntry, cfg LLMConfig, model, apiKey string) (llm.ClientConfig, error) {
	if cap, ok := eff.capabilityFor(model); ok && cap.MaxOutputTokens < 0 {
		return llm.ClientConfig{}, fmt.Errorf("model %q has a negative output maximum", model)
	} else if ok && cap.ContextLength < 0 {
		return llm.ClientConfig{}, fmt.Errorf("model %q has a negative context window", model)
	}
	e = limitModelOutput(e, model, eff)
	cc, err := a.entryTransport(e, providerAPIKey(e, apiKey, keyEnvFor(cfg.Provider, cfg.APIKeyEnv, e)))
	if err != nil {
		return llm.ClientConfig{}, err
	}
	cc.Model = model
	cc.ThinkingBudget = e.ThinkingBudget

	cc.ThinkingMode = thinkingShapeFor(eff, e, model)
	cc.ThinkingDisplay = e.ThinkingDisplay
	cc.ReplayReasoning = e.ReplayReasoning
	cc.ReasoningEffort = e.ReasoningEffort

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

func (a *App) birthEntry(name, endpoint, model string) (providerEntry, *effectiveCaps, error) {

	e := providerEntry{Name: name, URL: endpoint, DefaultModel: model, typedOnly: true}
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
		if endpoint != "" && !sameOrigin(endpoint, stored.URL) {

			if !stored.Local {
				return providerEntry{}, nil, birthAddressRefusal(stored, endpoint)
			}
			e.APIKey, e.APIKeyEnv, e.VendorKeyEnv = "", "", ""
			e.Credential, e.CredentialOptions = "", nil
			e.typedOnly = true
		}
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

func birthAddressRefusal(stored providerEntry, endpoint string) error {
	shown := func(raw string) string {
		if o := addressOrigin(raw); o != "" {
			return o
		}
		return raw
	}
	return &keyAddressError{fmt.Sprintf("provider %q is at %s, and this birth names %s. A birth asks a provider only at the address providers.json holds for it, except the operator's own local server, so nothing was asked. To birth at another address, change the address in providers.json.",
		stored.Name, shown(stored.URL), shown(endpoint))}
}

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

func (a *App) discoveredMeta(provider, model string) (m modelMeta, found bool, listed int, named bool) {
	a.providers.runtime.mu.Lock()
	defer a.providers.runtime.mu.Unlock()
	pr := a.providers.runtime.provStatus[provider]
	m, found = pr.meta[model]
	return m, found, len(pr.models), modelOffered(pr.models, pr.meta, model)
}

func (a *App) discoveredImageSupport(entry providerEntry, model string) *bool {
	a.providers.runtime.mu.RLock()
	defer a.providers.runtime.mu.RUnlock()
	pr := a.providers.runtime.provStatus[entry.Name]
	if pr.key != probeKey(entry) || pr.meta[model].Images == nil {
		return nil
	}
	value := *pr.meta[model].Images
	return &value
}

func markPreselect(out []dashboard.ProviderInfo) {
	for i := range out {
		if out[i].Credential != "" && out[i].Status == probeOK {
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

	if i.Refused {
		out.Expired = true
	} else if !i.ExpiresAt.IsZero() && !i.Renews {

		usable := i.ExpiresAt.Add(-oauth.ExpirySkew)
		out.ExpiresAt = usable.UTC().Format(time.RFC3339)
		out.Expired = !time.Now().Before(usable)
	}
	return out
}

const credentialWarnWindow = 30 * time.Minute

func (a *App) credentialWarning() string {
	entry := a.currentProvider()
	if entry.Credential == "" {
		return ""
	}
	src, err := a.credentialSource(entry.Credential, entry.CredentialOptions, entry.signIn)
	if err != nil {
		return ""
	}
	i := src.Info()
	now := time.Now()
	if i.Refused {
		return credentialWarningFor(entry.Credential, true, now, now)
	}
	if i.ExpiresAt.IsZero() || i.Renews {
		return ""
	}

	return credentialWarningFor(entry.Credential, i.Owned, i.ExpiresAt.Add(-oauth.ExpirySkew).Truncate(time.Second), now)
}

func credentialWarningFor(kind string, owned bool, usable, now time.Time) string {
	left := usable.Sub(now)
	if left > credentialWarnWindow {
		return ""
	}
	remedy := "refresh it with its own tool"
	if owned {
		remedy = "sign in again"
	}
	if left <= 0 {
		return fmt.Sprintf("the %s credential is no longer usable — %s; the identity cannot think until you do", kind, remedy)
	}
	return fmt.Sprintf("the %s credential becomes unusable in %d min — %s", kind, int(left.Minutes())+1, remedy)
}

func entryDialect(e providerEntry) llm.Dialect {
	if e.Credential != "" {
		if d := oauth.Dialect(e.Credential, e.CredentialOptions); d != "" {
			return llm.DialectFor(d)
		}
	}
	return llm.DialectFor(e.APIType)
}

func summaryFieldFor(e providerEntry) string {
	return llm.ReasoningSummaryField(entryDialect(e))
}

func displayOnCard(e providerEntry) string {
	d := entryDialect(e)
	if llm.ReasoningSummaryField(d) == "" {
		return e.ThinkingDisplay
	}
	plan := llm.PlanReasoningSummary(d, e.ThinkingDisplay)
	if !plan.Sent {
		return ""
	}
	return plan.Wire
}

func cardDialect(e providerEntry) llm.Dialect {
	if e.Credential != "" && e.CredentialOptions == nil {
		lent := providerRegistry{Providers: []providerEntry{e}}
		fillEmbeddedCredentialOptions(&lent)
		e = lent.Providers[0]
	}
	return entryDialect(e)
}

func displayFromCard(e providerEntry, stored *providerEntry) string {
	if stored == nil || llm.ReasoningSummaryField(entryDialect(*stored)) == "" {
		return e.ThinkingDisplay
	}
	if displayOnCard(*stored) == e.ThinkingDisplay {
		return stored.ThinkingDisplay
	}
	switch e.ThinkingDisplay {
	case "":
		return llm.SummaryOmitted
	case llm.ReasoningSummaryDefault(cardDialect(e)):
		return ""
	}
	return e.ThinkingDisplay
}

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

func seedModelsFor(name string) []string {
	if e := entryNamed(embeddedRegistry(), name); e != nil {
		return e.Models
	}
	return nil
}

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
