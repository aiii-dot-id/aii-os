package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/atomicfile"
	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/oauth"
	"io"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/aiii-dot-id/aii-os/config"
)

var embeddedProviders = configdir.Providers

type providerEntry struct {
	Name    string `json:"name"`
	APIType string `json:"api_type"`

	EffortLevels []string `json:"effort_levels,omitempty"`

	CatalogueAuthor string `json:"catalogue_author,omitempty"`

	VendorKeyEnv string `json:"vendor_key_env,omitempty"`
	URL          string `json:"url"`

	Formerly []formerRoute `json:"formerly,omitempty"`

	EmbeddingsModel string `json:"embeddings_model,omitempty"`

	Chat *bool `json:"chat,omitempty"`

	Speech    *speechBlock `json:"speech,omitempty"`
	APIKey    string       `json:"api_key,omitempty"`
	APIKeyEnv string       `json:"api_key_env,omitempty"`

	Credential              string `json:"credential,omitempty"`
	OAuth                   string `json:"oauth,omitempty"`
	signIn                  oauth.Provider
	CredentialOptionFormats map[string]string `json:"credential_option_formats,omitempty"`

	signInsSeen uint64

	typedOnly bool

	CredentialOptions map[string]string `json:"credential_options,omitempty"`

	ClaudeCode   *llm.ClaudeCodeProfile `json:"claude_code,omitempty"`
	DefaultModel string                 `json:"default_model,omitempty"`

	ContextLength   int    `json:"context_length,omitempty"`
	MaxOutputTokens int    `json:"max_output_tokens,omitempty"`
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
	ThinkingBudget  int    `json:"thinking_budget,omitempty"`

	ThinkingMode string `json:"thinking_mode,omitempty"`

	ThinkingDisplay string `json:"thinking_display,omitempty"`

	ReplayReasoning bool `json:"replay_reasoning,omitempty"`

	Temperature *float64 `json:"temperature,omitempty"`
	TopP        *float64 `json:"top_p,omitempty"`

	Extra        map[string]any   `json:"extra,omitempty"`
	Cache        *llm.CachePolicy `json:"cache,omitempty"`
	SubscribeURL string           `json:"subscribe_url,omitempty"`
	Default      bool             `json:"default,omitempty"`
	Models       []string         `json:"models,omitempty"`

	Local bool `json:"local,omitempty"`

	raw json.RawMessage
}

type formerRoute struct {
	APIType string `json:"api_type"`
	URL     string `json:"url"`
}

func (e providerEntry) MarshalJSON() ([]byte, error) {
	if e.raw != nil {
		return e.raw, nil
	}
	type fields providerEntry
	return json.Marshal(fields(e))
}

type modelCapability struct {
	MaxOutputTokens int `json:"max_output_tokens,omitempty"`

	ContextLength       int      `json:"context_length,omitempty"`
	CacheRetentions     []string `json:"cache_retentions,omitzero"`
	MaxCompletionTokens *bool    `json:"max_completion_tokens,omitempty"`

	Thinking         string `json:"thinking,omitempty"`
	CacheBreakpoints *bool  `json:"cache_breakpoints,omitempty"`

	Effort []string `json:"effort"`

	Images *bool `json:"images,omitempty"`

	ImageTokens    int   `json:"image_tokens,omitempty"`
	ImageMaxBytes  int64 `json:"image_max_bytes,omitempty"`
	ImagesMaxBytes int64 `json:"images_max_bytes,omitempty"`
	ImageMaxSide   int   `json:"image_max_side,omitempty"`
	ImagesMaxCount int   `json:"images_max_count,omitempty"`
}

type providerRegistry struct {
	ModelCatalogueURL *string `json:"model_catalogue_url,omitempty"`

	OAuth map[string]oauth.Provider `json:"oauth,omitempty"`

	Providers []providerEntry `json:"providers"`

	ModelCapabilities map[string]modelCapability `json:"model_capabilities,omitempty"`

	DialectEffortFloor map[string][]string `json:"dialect_effort_floor,omitempty"`

	lent map[string]*lentFacts

	broken []brokenEntry

	eff *effectiveCaps
}

func (a *App) providersPath() string {
	return providerFilePath(a.configSnapshot().SourcePath)
}

func providerFilePath(configPath string) string {
	return filepath.Join(filepath.Dir(configPath), ProvidersFileName)
}

func requiredCredentialOptions(credential string) []string {
	if credential == "" {
		return nil
	}
	for _, e := range embeddedRegistry().Providers {
		if e.Credential != credential || len(e.CredentialOptions) == 0 {
			continue
		}
		names := make([]string, 0, len(e.CredentialOptions))
		for k := range e.CredentialOptions {
			names = append(names, k)
		}
		sort.Strings(names)
		return names
	}
	return nil
}

func missingCredentialOptions(credential string, opts map[string]string) []string {
	var missing []string
	for _, name := range requiredCredentialOptions(credential) {
		if strings.TrimSpace(opts[name]) == "" {
			missing = append(missing, name)
		}
	}
	return missing
}

func fillEmbeddedCatalogueAuthors(reg *providerRegistry) {
	shipped := make(map[string]string)
	for _, e := range embeddedRegistry().Providers {
		if e.CatalogueAuthor != "" {
			shipped[e.Name] = e.CatalogueAuthor
		}
	}
	for i := range reg.Providers {
		e := &reg.Providers[i]
		if e.CatalogueAuthor != "" {
			continue
		}
		if a, ok := shipped[e.Name]; ok {
			e.CatalogueAuthor = a
			reg.lentTo(e.Name).author = true
		}
	}
}

func fillEmbeddedVendorKeyEnv(reg *providerRegistry) {
	for i := range reg.Providers {
		e := &reg.Providers[i]
		if e.VendorKeyEnv != "" {
			continue
		}
		if env, _, atHome := vendorsKey(e.Name, e.URL); atHome {
			e.VendorKeyEnv = env
			reg.lentTo(e.Name).keyEnv = true
		}
	}
}

func vendorKeyEnvOwner(env string) string {
	if env == "" {
		return ""
	}
	for _, e := range embeddedRegistry().Providers {
		if e.VendorKeyEnv == env {
			return e.Name
		}
	}
	return ""
}

func fillEmbeddedRoute(reg *providerRegistry) {
	for _, shipped := range embeddedRegistry().Providers {
		for _, former := range shipped.Formerly {
			for i := range reg.Providers {
				e := &reg.Providers[i]
				apiType := e.APIType
				if apiType == "" {
					apiType = "openai"
				}
				if e.Name != shipped.Name || e.Credential != "" || apiType != former.APIType || e.URL != former.URL {
					continue
				}
				lent := lentRoute{fromType: apiType, fromURL: e.URL, toType: shipped.APIType, toURL: shipped.URL}
				e.APIType, e.URL = shipped.APIType, shipped.URL
				if e.ContextLength == 0 {
					e.ContextLength, lent.context = shipped.ContextLength, shipped.ContextLength
				}
				if e.MaxOutputTokens == 0 {
					e.MaxOutputTokens, lent.output = shipped.MaxOutputTokens, shipped.MaxOutputTokens
				}

				if e.Cache != nil && llm.ValidateCachePolicy(*e.Cache, llm.DialectFor(shipped.APIType), false) != nil {
					lent.cache, e.Cache = e.Cache, nil
				}
				reg.lentTo(e.Name).route = lent
			}
		}
	}
}

func (r *providerRegistry) movedRouteNote(name string) string {
	l := r.lent[name]
	if l == nil || l.route.toURL == "" {
		return ""
	}
	rt := l.route
	note := fmt.Sprintf("provider %q: providers.json states the route an earlier release shipped (api_type %q at %s), which cannot serve this provider's models; it is served by api_type %q at %s for this run, and the file is left as it is", name, rt.fromType, rt.fromURL, rt.toType, rt.toURL)
	if rt.context != 0 || rt.output != 0 {
		note += fmt.Sprintf("; the file states no window or output allocation, so that route's are used (%d and %d tokens)", rt.context, rt.output)
	}
	if rt.cache != nil {
		note += "; the entry's cache setting is not carried, because that route takes none"
	}
	return note + ". To state a route of your own, set API TYPE and the endpoint on the provider's card."
}

func fillEmbeddedEffortLevels(reg *providerRegistry) {
	base := embeddedRegistry()
	eff := reg.effective()
	shipped := make(map[string][]string, len(base.Providers))
	for _, e := range base.Providers {
		if len(e.EffortLevels) > 0 {

			shipped[e.Name] = slices.Clone(e.EffortLevels)
		}
	}
	for i := range reg.Providers {
		e := &reg.Providers[i]
		if len(e.EffortLevels) > 0 {
			continue
		}
		levels, ok := shipped[e.Name]
		if !ok {

			levels, ok = eff.dialectFloor(e.APIType)
		}
		if ok {
			e.EffortLevels = levels
			reg.lentTo(e.Name).effort = true
		}
	}
}

func fillEmbeddedCredentialOptions(reg *providerRegistry) {

	base := embeddedRegistry()

	shipped := make(map[string]map[string]string, len(base.Providers))
	for _, e := range base.Providers {
		if e.Credential != "" && len(e.CredentialOptions) > 0 {
			if _, seen := shipped[e.Credential]; !seen {
				shipped[e.Credential] = e.CredentialOptions
			}
		}
	}
	for i := range reg.Providers {
		e := &reg.Providers[i]
		if e.Credential == "" {
			continue
		}
		defaults, ok := shipped[e.Credential]
		if !ok {
			continue
		}
		if e.CredentialOptions == nil {
			e.CredentialOptions = make(map[string]string, len(defaults))
		}
		for k, v := range defaults {
			if _, set := e.CredentialOptions[k]; !set {
				e.CredentialOptions[k] = v

				reg.lentTo(e.Name).option(k, v)
			}
		}
	}

	for _, shippedEntry := range base.Providers {
		shippedVersion := shippedEntry.CredentialOptions["client_version"]
		if shippedVersion == "" || len(shippedEntry.CredentialOptionFormats) == 0 {
			continue
		}
		for i := range reg.Providers {
			e := &reg.Providers[i]
			if e.Credential != shippedEntry.Credential {
				continue
			}
			if e.CredentialOptions == nil {
				e.CredentialOptions = map[string]string{}
			}
			ver := e.CredentialOptions["client_version"]
			if ver == shippedVersion {
				reg.lentTo(e.Name).option("client_version", ver)
			}
			formats := shippedEntry.CredentialOptionFormats
			if e.CredentialOptionFormats != nil {
				formats = e.CredentialOptionFormats
			}
			for key, format := range formats {
				value := strings.ReplaceAll(format, "{client_version}", ver)
				e.CredentialOptions[key] = value
				reg.lentTo(e.Name).option(key, value)
			}
		}
	}
}

type lentFacts struct {
	options                        map[string]string
	oauth                          string
	effort, author, speech, keyEnv bool
	route                          lentRoute
}

type lentRoute struct {
	fromType, fromURL string
	toType, toURL     string
	context, output   int
	cache             *llm.CachePolicy
}

func (r *providerRegistry) lentTo(name string) *lentFacts {
	if r.lent[name] == nil {
		if r.lent == nil {
			r.lent = map[string]*lentFacts{}
		}
		r.lent[name] = &lentFacts{}
	}
	return r.lent[name]
}

func (l *lentFacts) option(key, value string) {
	if l.options == nil {
		l.options = map[string]string{}
	}
	l.options[key] = value
}

func cloneLent(in map[string]*lentFacts) map[string]*lentFacts {
	if in == nil {
		return nil
	}
	out := make(map[string]*lentFacts, len(in))
	for name, l := range in {
		c := *l
		c.options = maps.Clone(l.options)
		out[name] = &c
	}
	return out
}

func (r *providerRegistry) operatorView() *providerRegistry {
	if len(r.lent) == 0 {
		return r
	}

	cp := *r
	cp.Providers = slices.Clone(r.Providers)
	for i := range cp.Providers {
		e := &cp.Providers[i]
		l := r.lent[e.Name]
		if l == nil {
			continue
		}
		if l.oauth != "" && e.OAuth == l.oauth {
			e.OAuth = ""
		}
		if l.effort {
			e.EffortLevels = nil
		}
		if l.author {
			e.CatalogueAuthor = ""
		}
		if l.keyEnv {
			e.VendorKeyEnv = ""
		}
		if l.speech {
			e.Speech = nil
		}

		if rt := l.route; rt.toURL != "" {
			if e.APIType == rt.toType && e.URL == rt.toURL {
				e.APIType, e.URL = rt.fromType, rt.fromURL
			}
			if rt.context != 0 && e.ContextLength == rt.context {
				e.ContextLength = 0
			}
			if rt.output != 0 && e.MaxOutputTokens == rt.output {
				e.MaxOutputTokens = 0
			}
			if rt.cache != nil && e.Cache == nil {
				e.Cache = rt.cache
			}
		}
		if len(l.options) == 0 || len(e.CredentialOptions) == 0 {
			continue
		}
		kept := make(map[string]string, len(e.CredentialOptions))
		for k, v := range e.CredentialOptions {
			if lentValue, was := l.options[k]; was && lentValue == v {
				continue
			}
			kept[k] = v
		}
		if len(kept) == 0 {
			kept = nil
		}
		e.CredentialOptions = kept
	}
	return &cp
}

func (r *providerRegistry) modelCatalogueURL() string {
	base := embeddedRegistry().ModelCatalogueURL
	if r != nil && r.ModelCatalogueURL != nil {
		base = r.ModelCatalogueURL
	}
	if base == nil {
		return ""
	}
	return strings.TrimRight(*base, "/")
}

func (a *App) loadProviders() (*providerRegistry, error) {
	return loadProvidersFile(a.providersPath())
}

func loadProvidersFile(path string) (*providerRegistry, error) {
	data, err := atomicfile.ReadFile(path)
	if os.IsNotExist(err) {
		if _, werr := writeFileAtomic(path, scaffoldProviders()); werr != nil {
			return nil, fmt.Errorf("cannot scaffold %s: %w", path, werr)
		}
		logsink.Info("providers.start", "No providers file found. Created default %s — user-editable, like config.json.", path)
		data, err = atomicfile.ReadFile(path)
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	var file struct {
		providerRegistry
		Providers []json.RawMessage `json:"providers"`
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&file); err != nil {
		return nil, fmt.Errorf("%s is invalid: %w", path, err)
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return nil, fmt.Errorf("%s is invalid: trailing JSON data", path)
	}
	reg, err := admitEntries(file.providerRegistry, file.Providers)
	if err != nil {
		return nil, fmt.Errorf("%s is invalid: %w", path, err)
	}
	return reg, nil
}

func validateEntries(reg *providerRegistry) error {
	names := make(map[string]struct{}, len(reg.Providers))
	defaultName := ""
	for i := range reg.Providers {
		e := &reg.Providers[i]
		if e.Name == "" {
			return fmt.Errorf("provider name is empty")
		}
		if _, exists := names[e.Name]; exists {
			return fmt.Errorf("duplicate provider %q", e.Name)
		}
		names[e.Name] = struct{}{}
		if e.Default {
			if defaultName != "" {
				return fmt.Errorf("providers %q and %q are both default", defaultName, e.Name)
			}
			defaultName = e.Name
		}
		if err := normalizeProviderAPIType(e); err != nil {
			return err
		}
		if err := validateSpeech(e); err != nil {
			return err
		}
		if err := validateRole(e); err != nil {
			return err
		}
	}
	return nil
}

func normalizeProviderAPIType(e *providerEntry) error {
	if e.APIType == "" {
		e.APIType = "openai"
	}
	if e.APIType != "openai" && e.APIType != "anthropic" && e.APIType != "gemini" {
		return fmt.Errorf("provider %q has unknown api_type %q (want openai, anthropic or gemini)", e.Name, e.APIType)
	}
	return nil
}

func saveProvidersFile(path string, reg *providerRegistry) (bool, error) {
	data, err := json.MarshalIndent(withBrokenEntries(reg.operatorView()), "", "  ")
	if err != nil {
		return false, err
	}
	return writeFileAtomic(path, data)
}

func (a *App) setProvider(e providerEntry, keepAPIKey bool) error {
	return a.setProviderWith(e, keepAPIKey, false, false)
}

func (a *App) setProviderFromCard(e providerEntry, keepAPIKey, keepEmbeddingsModel bool) error {
	return a.setProviderWith(e, keepAPIKey, true, keepEmbeddingsModel)
}

func (a *App) setProviderWith(e providerEntry, keepAPIKey, fromCard, keepEmbeddingsModel bool) error {
	if e.Name == "" || e.URL == "" {
		return fmt.Errorf("a provider needs at least a name and a url")
	}
	if !validProviderURL(e.URL) {
		return fmt.Errorf("%q is not a valid provider URL (http(s)://host[/path])", e.URL)
	}
	if e.APIKeyEnv != "" && !validEnvName(e.APIKeyEnv) {
		return fmt.Errorf("api_key_env %q is not an environment variable name (example: ZAI_API_KEY); put the credential in API KEY, not API KEY ENV", e.APIKeyEnv)
	}
	if err := normalizeProviderAPIType(&e); err != nil {
		return err
	}
	if err := validateModelWindow(e, e.DefaultModel); err != nil {
		return err
	}
	if e.Credential == "none" {
		e.Credential = ""
	}
	return a.changeProviders(e.Name, func(reg *providerRegistry) error {
		found := false
		for i := range reg.Providers {
			if reg.Providers[i].Name != e.Name {
				continue
			}
			if fromCard {
				if keepEmbeddingsModel {
					e.EmbeddingsModel = reg.Providers[i].EmbeddingsModel
				}
				e.Local = reg.Providers[i].Local
				e.CatalogueAuthor = reg.Providers[i].CatalogueAuthor
				e.ReplayReasoning = reg.Providers[i].ReplayReasoning

				if l := reg.lent[e.Name]; l == nil || !l.keyEnv {
					e.VendorKeyEnv = reg.Providers[i].VendorKeyEnv
				}
			}

			keepsStoredKey := e.APIKey == "" && keepAPIKey && reg.Providers[i].APIKey != ""
			if fromCard {
				if err := cardKeyRefusal(a.cfg, &reg.Providers[i], e, keepsStoredKey); err != nil {
					return err
				}
			}
			if e.APIKey == "" && keepAPIKey {
				e.APIKey = reg.Providers[i].APIKey
			}

			if e.CredentialOptions == nil && e.Credential == reg.Providers[i].Credential {
				e.CredentialOptions = reg.Providers[i].CredentialOptions
				if e.OAuth == "" {
					e.OAuth = reg.Providers[i].OAuth
				}
				e.CredentialOptionFormats = reg.Providers[i].CredentialOptionFormats
				if e.ClaudeCode == nil {
					e.ClaudeCode = reg.Providers[i].ClaudeCode
				}
			}

			if e.EffortLevels == nil {
				e.EffortLevels = reg.Providers[i].EffortLevels
			}

			if e.Speech == nil {
				e.Speech = reg.Providers[i].Speech
			}

			if e.Chat == nil {
				e.Chat = reg.Providers[i].Chat
			}
			if fromCard {
				e.ThinkingDisplay = displayFromCard(e, &reg.Providers[i])
			}
			reg.Providers[i] = e
			found = true
			break
		}
		if !found {
			if fromCard {
				if err := cardKeyRefusal(a.cfg, nil, e, false); err != nil {
					return err
				}
				e.ThinkingDisplay = displayFromCard(e, nil)
			}
			reg.Providers = append(reg.Providers, e)
		}
		if e.Default {
			for i := range reg.Providers {
				if reg.Providers[i].Name != e.Name {
					reg.Providers[i].Default = false
				}
			}
		}
		if fromCard {

			if l := reg.lent[e.Name]; l != nil {
				l.keyEnv = false
			}
			fillEmbeddedVendorKeyEnv(reg)
		}
		return nil
	})
}

type keyAddressError struct{ msg string }

func (e *keyAddressError) Error() string { return e.msg }

func movedKeyRefusal(cfg *Config, stored, next providerEntry, keepsStoredKey bool) error {
	if sameOrigin(stored.URL, next.URL) {
		return nil
	}
	shown := func(raw string) string {
		if o := addressOrigin(raw); o != "" {
			return o
		}
		return raw
	}
	refuse := func(what, remedy string) error {
		return &keyAddressError{fmt.Sprintf("provider %q: the address changed from %s to %s, and %s. %s Nothing was saved.",
			next.Name, shown(stored.URL), shown(next.URL), what, remedy)}
	}
	switch {
	case keepsStoredKey:
		return refuse("the stored key was entered for the first",
			"Enter the key again for the new address, or clear the stored key.")
	case next.APIKeyEnv != "" && next.APIKeyEnv == stored.APIKeyEnv:
		return refuse(fmt.Sprintf("this entry's key is read from the variable %s, which providers.json names for it (api_key_env) at the first", next.APIKeyEnv),
			"Change the address in providers.json, beside the variable.")
	case next.VendorKeyEnv != "":

		return refuse(fmt.Sprintf("this entry's key is read from the variable %s, which providers.json names for it (vendor_key_env) at the first", next.VendorKeyEnv),
			"Change the address in providers.json, beside the variable.")
	case next.Credential != "" && !atCredentialsAddress(next):
		return refuse(fmt.Sprintf("this entry uses the %s credential, which the card does not send to another address", next.Credential),
			"Change the address in providers.json, or save the new address with no credential.")
	}
	for _, b := range blockKeyEnvs(cfg, next) {
		if vendorKeyEnvOwner(b.env) == "" {
			return refuse(fmt.Sprintf("this entry's key is read from the variable %s, which config.json names for it (%s)", b.env, b.key),
				"Change the address in providers.json.")
		}
	}
	return nil
}

func atCredentialsAddress(e providerEntry) bool {
	for _, shipped := range embeddedRegistry().Providers {
		if shipped.Credential == e.Credential && sameOrigin(shipped.URL, e.URL) {
			return true
		}
	}
	return false
}

func credentialAddressRefusal(e providerEntry) error {
	if e.Credential == "" || e.CredentialOptions["base_url"] != "" || atCredentialsAddress(e) {
		return nil
	}
	home := credentialsHome(e.Credential)
	if home == "" {
		return nil
	}
	at := addressOrigin(e.URL)
	if at == "" {
		at = "an address that is not a URL"
	}
	return &keyAddressError{fmt.Sprintf("provider %q: the %s credential was not sent: it goes only to %s, the address this release ships for it, and this entry is at %s. "+
		"To send it to another address, name that address on this entry in providers.json (credential_options.base_url)",
		e.Name, e.Credential, home, at)}
}

func cardKeyRefusal(cfg *Config, stored *providerEntry, next providerEntry, keepsStoredKey bool) error {
	named := ""
	if stored != nil {
		named = stored.APIKeyEnv
	}
	if next.APIKeyEnv != "" && next.APIKeyEnv != named {
		return &keyAddressError{fmt.Sprintf("provider %q: this save names the variable %s for the entry's key (api_key_env), and providers.json does not name it for this entry. "+
			"A save from the dashboard keeps the variable the file names, or clears it: a new one is named in providers.json, beside the entry's address. Nothing was saved.",
			next.Name, next.APIKeyEnv)}
	}
	if next.Credential != "" && (stored == nil || next.Credential != stored.Credential) && !atCredentialsAddress(next) {
		at := addressOrigin(next.URL)
		if home := credentialsHome(next.Credential); home != "" {
			return &keyAddressError{fmt.Sprintf("provider %q: this save attaches the %s credential, and the entry is at %s. "+
				"The dashboard attaches that credential only to an entry at %s, the address it is for; for another address, name the credential on the entry in providers.json. Nothing was saved.",
				next.Name, next.Credential, at, home)}
		}
		return &keyAddressError{fmt.Sprintf("provider %q: this save attaches the %s credential, and the entry is at %s. "+
			"The dashboard attaches a credential only at the address this release ships for it, and it ships none for this one: name the credential on the entry in providers.json. Nothing was saved.",
			next.Name, next.Credential, at)}
	}
	if stored == nil {
		return nil
	}
	return movedKeyRefusal(cfg, *stored, next, keepsStoredKey)
}

func credentialsHome(credential string) string {
	for _, shipped := range embeddedRegistry().Providers {
		if shipped.Credential == credential {
			return addressOrigin(shipped.URL)
		}
	}
	return ""
}

type blockKeyEnv struct{ key, env string }

func blockKeyEnvs(cfg *Config, e providerEntry) []blockKeyEnv {
	if cfg == nil {
		return nil
	}
	var out []blockKeyEnv
	if env := keyEnvFor(cfg.LLM.Provider, cfg.LLM.APIKeyEnv, e); env != "" {
		out = append(out, blockKeyEnv{"llm.api_key_env", env})
	}
	if stt := cfg.Speech.STT; stt.Provider == e.Name && stt.APIKeyEnv != "" {
		out = append(out, blockKeyEnv{"speech.stt.api_key_env", stt.APIKeyEnv})
	}
	if tts := cfg.Speech.TTS; tts.Provider == e.Name && tts.APIKeyEnv != "" {
		out = append(out, blockKeyEnv{"speech.tts.api_key_env", tts.APIKeyEnv})
	}
	return out
}

func (a *App) deleteProvider(name string) error {
	return a.changeProviders(name, func(reg *providerRegistry) error {
		i := slices.IndexFunc(reg.Providers, func(e providerEntry) bool { return e.Name == name })
		if i < 0 {
			return fmt.Errorf("no provider named %q", name)
		}
		reg.Providers = slices.Delete(reg.Providers, i, i+1)
		for j := range reg.broken {
			if reg.broken[j].after > i {
				reg.broken[j].after--
			}
		}
		return nil
	})
}

func candidateRegistry(before *providerRegistry, mutate func(*providerRegistry) error) (*providerRegistry, error) {
	cp := *before
	cp.Providers = append([]providerEntry(nil), before.Providers...)
	cp.broken = slices.Clone(before.broken)
	cp.OAuth = cloneOAuth(before.OAuth)
	cp.ModelCapabilities = cloneCapabilities(before.ModelCapabilities)
	cp.DialectEffortFloor = cloneFloors(before.DialectEffortFloor)
	cp.lent = cloneLent(before.lent)
	candidate := &cp
	if err := mutate(candidate); err != nil {
		return nil, err
	}
	candidate.eff = newEffectiveCaps(candidate)
	if err := bindOAuth(candidate); err != nil {
		return nil, err
	}
	if err := validateEntries(candidate); err != nil {
		return nil, err
	}
	return candidate, nil
}

func cloneCapabilities(in map[string]modelCapability) map[string]modelCapability {
	if in == nil {
		return nil
	}
	out := make(map[string]modelCapability, len(in))
	for k, v := range in {
		v = copyModelCapability(v)
		out[k] = v
	}
	return out
}

func cloneFloors(in map[string][]string) map[string][]string {
	if in == nil {
		return nil
	}
	out := make(map[string][]string, len(in))
	for k, v := range in {
		out[k] = slices.Clone(v)
	}
	return out
}

func (a *App) changeProviders(name string, mutate func(*providerRegistry) error) error {
	path := a.providersPath()
	a.cfgMu.Lock()
	before, err := loadProvidersFile(path)
	if err != nil {
		a.cfgMu.Unlock()
		return err
	}
	candidate, err := candidateRegistry(before, mutate)
	if err != nil {
		a.cfgMu.Unlock()
		return err
	}
	if reflect.DeepEqual(before, candidate) && a.providerRuntimeMatches(candidate) {
		a.cfgMu.Unlock()
		return nil
	}
	cfg := *a.cfg

	persist := func(reload bool) error {
		published, persistErr := saveProvidersFile(path, candidate)
		a.cfgMu.Unlock()
		if published {
			a.clearProviderStatus(name)
			if reload {

				a.runBackground(a.reloadConfig)
			}
		}
		if persistErr != nil {
			if published {
				return fmt.Errorf("providers were published but directory durability is unconfirmed: %w", persistErr)
			}
			return fmt.Errorf("persist providers: %w", persistErr)
		}
		return nil
	}
	if a.llmSwap == nil {
		return persist(false)
	}

	oldEntry, oldErr := selectProvider(cfg.LLM, before)
	nextEntry, newErr := selectProvider(cfg.LLM, candidate)
	if newErr != nil {
		if oldErr == nil {
			a.cfgMu.Unlock()
			return fmt.Errorf("provider change refused: %w; current substrate kept", newErr)
		}

		return persist(false)
	}
	runtimeChanged := oldErr != nil || oldEntry.Name != nextEntry.Name ||
		!sameProviderRuntime(*oldEntry, *nextEntry, cfg.LLM.Model != "") || !a.providerRuntimeMatches(before)

	model := cfg.LLM.Model
	if model == "" {
		model = nextEntry.DefaultModel
	}

	atTurnBoundary := runtimeChanged && oldErr == nil && oldEntry.Name == nextEntry.Name &&
		effortOnlyDeclaredChange(*oldEntry, *nextEntry, cfg.LLM.Model != "", candidate.effective(), model) &&
		(!candidate.effective().effortIsShipped(model) || vendorsOwnEntry(*nextEntry)) &&
		a.providerRuntimeMatchesBarEffort(before)
	if !runtimeChanged || atTurnBoundary {
		return persist(atTurnBoundary)
	}
	a.cfgMu.Unlock()

	a.askWindowIfUndeclaredIn(candidate, cfg.LLM)
	newCC, resolvedEntry, err := a.resolveLLMConfig(cfg.LLM, candidate)
	if err != nil {
		return fmt.Errorf("provider change refused: %w; current substrate kept", err)
	}
	candidateBudget, _ := promptBudgetFor(resolvedEntry, cfg.Prompt.MaxTokens)
	client := a.newLLMClient(newCC, candidateBudget)
	measured, err := a.probeSubstrate(client, newCC, resolvedEntry, candidate, cfg.LLM.ProbeTimeoutSeconds)
	if err != nil {
		return err
	}

	if a.bgCtx == nil {
		return fmt.Errorf("provider change refused: application lifecycle is unavailable")
	}
	if err := a.acquireTurn(a.bgCtx); err != nil {
		return err
	}
	published := false
	commitErr := func() error {
		a.cfgMu.Lock()
		defer a.cfgMu.Unlock()
		current, err := loadProvidersFile(path)
		if err != nil {
			return fmt.Errorf("recheck providers: %w", err)
		}
		if !reflect.DeepEqual(*a.cfg, cfg) || !reflect.DeepEqual(current, before) {
			return fmt.Errorf("configuration changed while the provider was checked; retry")
		}
		var persistErr error
		published, persistErr = saveProvidersFile(path, candidate)
		if persistErr != nil && !published {
			return fmt.Errorf("persist providers: %w", persistErr)
		}
		if published {
			a.activateLLMRuntime(client, resolvedEntry, cfg.Prompt.MaxTokens)
			a.setSubstrateCapability(measured)
		}
		if persistErr != nil {
			return fmt.Errorf("providers were published and the live client was activated, but directory durability is unconfirmed: %w", persistErr)
		}
		return nil
	}()
	a.releaseTurn()
	if published {
		a.clearProviderStatus(name)
	}
	if commitErr != nil {
		return commitErr
	}

	logsink.Info("providers.decision", "provider %q edited — validated live client activated (model %s)", name, client.ModelName())
	return nil
}

type effectiveCaps struct {
	models map[string]modelCapability
	floors map[string][]string

	shippedEffort map[string]bool
}

func (e *effectiveCaps) effortIsShipped(model string) bool {
	return e != nil && e.shippedEffort[model]
}

var (
	embedOnce sync.Once
	embedReg  providerRegistry
)

func embeddedRegistry() *providerRegistry {
	embedOnce.Do(func() {
		if err := json.Unmarshal(embeddedProviders, &embedReg); err != nil {

			embedReg = providerRegistry{}
		}
	})
	return &embedReg
}

func newEffectiveCaps(reg *providerRegistry) *effectiveCaps {
	base := embeddedRegistry()
	s := &effectiveCaps{
		models:        make(map[string]modelCapability, len(base.ModelCapabilities)),
		floors:        make(map[string][]string, len(base.DialectEffortFloor)),
		shippedEffort: make(map[string]bool, len(base.ModelCapabilities)),
	}
	for k, v := range base.ModelCapabilities {
		s.models[k] = copyModelCapability(v)
		s.shippedEffort[k] = len(v.Effort) > 0
	}
	for k, v := range base.DialectEffortFloor {
		s.floors[k] = v
	}
	if reg != nil {
		for k, v := range reg.ModelCapabilities {
			base := s.models[k]

			if v.CacheBreakpoints == nil {
				v.CacheBreakpoints = base.CacheBreakpoints
			}
			if v.MaxCompletionTokens == nil {
				v.MaxCompletionTokens = base.MaxCompletionTokens
			}
			if v.MaxOutputTokens == 0 {
				v.MaxOutputTokens = base.MaxOutputTokens
			}
			if v.ContextLength == 0 {
				v.ContextLength = base.ContextLength
			}
			if v.CacheRetentions == nil {
				v.CacheRetentions = base.CacheRetentions
			}
			if v.Images == nil {
				v.Images = base.Images
			}
			if v.ImageTokens == 0 {
				v.ImageTokens = base.ImageTokens
			}
			if v.ImageMaxBytes == 0 {
				v.ImageMaxBytes = base.ImageMaxBytes
			}
			if v.ImagesMaxBytes == 0 {
				v.ImagesMaxBytes = base.ImagesMaxBytes
			}
			if v.ImageMaxSide == 0 {
				v.ImageMaxSide = base.ImageMaxSide
			}
			if v.ImagesMaxCount == 0 {
				v.ImagesMaxCount = base.ImagesMaxCount
			}

			if v.Effort == nil {
				v.Effort = base.Effort
			} else {
				s.shippedEffort[k] = false
			}
			s.models[k] = copyModelCapability(v)
		}
		for k, v := range reg.DialectEffortFloor {
			s.floors[k] = v
		}
	}
	return s
}

func (r *providerRegistry) effective() *effectiveCaps {
	if r != nil && r.eff != nil {
		return r.eff
	}
	return newEffectiveCaps(r)
}

func (s *effectiveCaps) capabilityFor(model string) (modelCapability, bool) {
	if s == nil {
		return modelCapability{}, false
	}
	c, ok := s.models[model]
	if !ok {
		return modelCapability{}, false
	}
	return copyModelCapability(c), true
}

func (s *effectiveCaps) dialectFloor(apiType string) ([]string, bool) {
	if s == nil {
		return nil, false
	}
	levels, ok := s.floors[apiType]
	if !ok {
		return nil, false
	}

	return slices.Clone(levels), true
}

func effortLevelsFor(eff *effectiveCaps, e providerEntry, model string) []string {
	if c, ok := eff.capabilityFor(model); ok {

		return c.Effort
	}
	return e.EffortLevels
}

func thinkingShapeFor(eff *effectiveCaps, e providerEntry, model string) string {
	if e.ThinkingMode == "off" {
		return "off"
	}
	if c, ok := eff.capabilityFor(model); ok && c.Thinking != "" {
		return c.Thinking
	}
	return e.ThinkingMode
}

func (c modelCapability) cacheBreakpoints() bool {
	return c.CacheBreakpoints != nil && *c.CacheBreakpoints
}
func (c modelCapability) modernOutputLimit() bool {
	return c.MaxCompletionTokens != nil && *c.MaxCompletionTokens
}

func copyModelCapability(c modelCapability) modelCapability {
	c.Effort = slices.Clone(c.Effort)
	c.CacheRetentions = slices.Clone(c.CacheRetentions)
	if c.CacheBreakpoints != nil {
		v := *c.CacheBreakpoints
		c.CacheBreakpoints = &v
	}
	if c.MaxCompletionTokens != nil {
		v := *c.MaxCompletionTokens
		c.MaxCompletionTokens = &v
	}
	if c.Images != nil {
		v := *c.Images
		c.Images = &v
	}
	return c
}

func cloneOAuth(in map[string]oauth.Provider) map[string]oauth.Provider {
	if in == nil {
		return nil
	}
	out := make(map[string]oauth.Provider, len(in))
	for name := range in {
		out[name], _ = oauth.ProviderTemplate(name, in)
	}
	return out
}
func (r *providerRegistry) oauthContracts() map[string]oauth.Provider {
	out := cloneOAuth(embeddedRegistry().OAuth)
	if out == nil {
		out = map[string]oauth.Provider{}
	}
	if r != nil {
		for name := range r.OAuth {
			out[name], _ = oauth.ProviderTemplate(name, r.OAuth)
		}
	}
	return out
}
func bindOAuth(reg *providerRegistry) error {
	catalog := reg.oauthContracts()
	defaults := map[string]string{}
	for _, e := range embeddedRegistry().Providers {
		if e.OAuth != "" {
			defaults[e.Credential] = e.OAuth
		}
	}
	for i := range reg.Providers {
		e := &reg.Providers[i]
		if e.OAuth == "" && defaults[e.Credential] != "" {
			e.OAuth = defaults[e.Credential]
			reg.lentTo(e.Name).oauth = e.OAuth
		}
		e.signIn = oauth.Provider{}
		if e.OAuth != "" {
			p, ok := oauth.ProviderTemplate(e.OAuth, catalog)
			if !ok {
				return fmt.Errorf("provider %q names unknown OAuth contract %q", e.Name, e.OAuth)
			}
			e.signIn = p
		}

		params, err := oauth.ParamsFromOptions(e.signIn.Params(), e.CredentialOptions)
		if err != nil {
			return fmt.Errorf("provider %q: %w", e.Name, err)
		}
		e.signIn.ClientID = params.ClientID
		e.signIn.AuthorizeURL = params.AuthorizeURL
		e.signIn.TokenURL = params.TokenURL
		e.signIn.RedirectURI = params.RedirectURI
		e.signIn.BaseScopes = strings.Fields(params.Scope)
		e.signIn.AuthorizeParams = params.AuthorizeParams
		e.signIn.TokenParams = params.TokenParams
		e.signIn.ResourceHeaders = params.ResourceHeaders
		e.signIn.ClaimHeaders = params.ClaimHeaders
	}
	return nil
}
func (a *App) oauthContracts() (map[string]oauth.Provider, error) {
	reg, err := a.loadProviders()
	if err != nil {
		return nil, err
	}
	return reg.oauthContracts(), nil
}

func (a *App) wireProviderHooks(h *dashboard.WSHandler) {

	h.GetProviders = a.providerDirectory
	h.SetProvider = a.setProviderInfo
	h.SetEffort = a.setActiveEffort
	h.DeleteProvider = a.deleteProvider
	h.RepairProvider = a.repairBrokenProvider
	h.RemoveBrokenProvider = a.removeBrokenProvider

	h.DiscoverModels = a.discoverModels
}

func (a *App) discoverModels(provider, baseURL, apiKey string) (models []string, needsKey bool, err error) {
	reg, err := a.loadProviders()
	if err != nil {
		return nil, false, err
	}

	models, err = a.discoverForProvider(a.lifetime(), reg, provider, baseURL, apiKey)
	return models, errors.Is(err, errAuthRequired), err
}
