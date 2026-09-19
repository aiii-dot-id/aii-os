package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/oauth"
	"io"
	"log"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/aiii-dot-id/aii-os/config"
)

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
// .
// .
// .

// .
// .
var embeddedProviders = configdir.Providers

// .
type providerEntry struct {
	Name    string `json:"name"`
	APIType string `json:"api_type"`
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
	// .
	EffortLevels []string `json:"effort_levels,omitempty"`
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
	CatalogueAuthor string `json:"catalogue_author,omitempty"`
	URL             string `json:"url"`
	// .
	// .
	// .
	// .
	EmbeddingsModel string `json:"embeddings_model,omitempty"`
	// .
	// .
	// .
	// .
	// .
	// .
	Chat *bool `json:"chat,omitempty"`
	// .
	// .
	// .
	Speech    *speechBlock `json:"speech,omitempty"`
	APIKey    string       `json:"api_key,omitempty"`
	APIKeyEnv string       `json:"api_key_env,omitempty"`
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	Credential              string `json:"credential,omitempty"`
	OAuth                   string `json:"oauth,omitempty"`
	signIn                  oauth.Provider
	CredentialOptionFormats map[string]string `json:"credential_option_formats,omitempty"`
	// .
	// .
	// .
	CredentialOptions map[string]string `json:"credential_options,omitempty"`
	DefaultModel      string            `json:"default_model,omitempty"`
	// .
	// .
	// .
	ContextLength   int    `json:"context_length,omitempty"`
	MaxOutputTokens int    `json:"max_output_tokens,omitempty"`
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
	ThinkingBudget  int    `json:"thinking_budget,omitempty"`
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
	// .
	// .
	ThinkingMode string `json:"thinking_mode,omitempty"`
	// .
	// .
	ThinkingDisplay string `json:"thinking_display,omitempty"`
	// .
	// .
	Temperature *float64 `json:"temperature,omitempty"`
	TopP        *float64 `json:"top_p,omitempty"`
	// .
	// .
	// .
	Extra        map[string]any   `json:"extra,omitempty"`
	Cache        *llm.CachePolicy `json:"cache,omitempty"`
	SubscribeURL string           `json:"subscribe_url,omitempty"`
	Default      bool             `json:"default,omitempty"`
	Models       []string         `json:"models,omitempty"`
	// .
	// .
	// .
	// .
	// .
	Local bool `json:"local,omitempty"`
	// .
	// .
	raw json.RawMessage
}

// .
// .
func (e providerEntry) MarshalJSON() ([]byte, error) {
	if e.raw != nil {
		return e.raw, nil
	}
	type fields providerEntry
	return json.Marshal(fields(e))
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
// .
// .
// .
type modelCapability struct {
	MaxOutputTokens     int      `json:"max_output_tokens,omitempty"`
	CacheRetentions     []string `json:"cache_retentions,omitzero"`
	MaxCompletionTokens *bool    `json:"max_completion_tokens,omitempty"`
	// .
	// .
	// .
	Thinking         string `json:"thinking,omitempty"`
	CacheBreakpoints *bool  `json:"cache_breakpoints,omitempty"`
	// .
	// .
	// .
	Effort []string `json:"effort"`
}

type providerRegistry struct {
	// .
	// .
	ModelCatalogueURL *string `json:"model_catalogue_url,omitempty"`

	OAuth       map[string]oauth.Provider `json:"oauth,omitempty"`
	filledOAuth map[string]string

	Providers []providerEntry `json:"providers"`

	// .
	// .
	ModelCapabilities map[string]modelCapability `json:"model_capabilities,omitempty"`

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
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	DialectEffortFloor map[string][]string `json:"dialect_effort_floor,omitempty"`

	// .
	// .
	// .
	// .
	filled map[string]map[string]string
	// .
	// .
	// .
	// .
	filledEffort map[string]bool
	filledAuthor map[string]bool
	// .
	// .
	filledSpeech map[string]bool
	// .
	// .
	// .
	broken []brokenEntry
	// .
	// .
	// .
	eff *effectiveCaps
}

// .
// .
// .
// .
func (a *App) providersPath() string {
	return providerFilePath(a.configSnapshot().SourcePath)
}

func providerFilePath(configPath string) string {
	return filepath.Join(filepath.Dir(configPath), "providers.json")
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
// .
// .
// .
// .
// .
// .
// .
func requiredCredentialOptions(credential string) []string {
	if credential == "" {
		return nil
	}
	var base providerRegistry
	if err := json.Unmarshal(embeddedProviders, &base); err != nil {
		return nil
	}
	for _, e := range base.Providers {
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

// .
// .
// .
// .
func missingCredentialOptions(credential string, opts map[string]string) []string {
	var missing []string
	for _, name := range requiredCredentialOptions(credential) {
		if strings.TrimSpace(opts[name]) == "" {
			missing = append(missing, name)
		}
	}
	return missing
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
// .
// .
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
			if reg.filledAuthor == nil {
				reg.filledAuthor = map[string]bool{}
			}
			reg.filledAuthor[e.Name] = true
		}
	}
}

func fillEmbeddedEffortLevels(reg *providerRegistry) {
	base := embeddedRegistry()
	eff := reg.effective()
	shipped := make(map[string][]string, len(base.Providers))
	for _, e := range base.Providers {
		if len(e.EffortLevels) > 0 {
			// .
			// .
			shipped[e.Name] = cloneLevels(e.EffortLevels)
		}
	}
	for i := range reg.Providers {
		e := &reg.Providers[i]
		if len(e.EffortLevels) > 0 {
			continue
		}
		levels, ok := shipped[e.Name]
		if !ok {
			// .
			// .
			// .
			// .
			// .
			// .
			levels, ok = eff.dialectFloor(e.APIType)
		}
		if ok {
			e.EffortLevels = levels
			if reg.filledEffort == nil {
				reg.filledEffort = map[string]bool{}
			}
			reg.filledEffort[e.Name] = true
		}
	}
}

func fillEmbeddedCredentialOptions(reg *providerRegistry) {
	// .
	// .
	// .
	base := embeddedRegistry()
	// .
	// .
	// .
	// .
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
				// .
				if reg.filled == nil {
					reg.filled = map[string]map[string]string{}
				}
				if reg.filled[e.Name] == nil {
					reg.filled[e.Name] = map[string]string{}
				}
				reg.filled[e.Name][k] = v
			}
		}
	}
	// .
	// .
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
			if reg.filled == nil {
				reg.filled = map[string]map[string]string{}
			}
			if reg.filled[e.Name] == nil {
				reg.filled[e.Name] = map[string]string{}
			}
			if e.CredentialOptions == nil {
				e.CredentialOptions = map[string]string{}
			}
			ver := e.CredentialOptions["client_version"]
			if ver == shippedVersion {
				reg.filled[e.Name]["client_version"] = ver
			}
			formats := shippedEntry.CredentialOptionFormats
			if e.CredentialOptionFormats != nil {
				formats = e.CredentialOptionFormats
			}
			for key, format := range formats {
				value := strings.ReplaceAll(format, "{client_version}", ver)
				e.CredentialOptions[key] = value
				reg.filled[e.Name][key] = value
			}
		}
	}
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
// .
// .
func stripEmbeddedFills(reg *providerRegistry) *providerRegistry {
	// .
	// .
	// .
	// .
	if len(reg.filled) == 0 && len(reg.filledEffort) == 0 && len(reg.filledOAuth) == 0 &&
		len(reg.filledAuthor) == 0 && len(reg.filledSpeech) == 0 {
		return reg
	}
	// .
	// .
	// .
	// .
	// .
	// .
	cp := *reg
	cp.Providers = append([]providerEntry(nil), reg.Providers...)
	out := &cp
	for i := range out.Providers {
		e := &out.Providers[i]
		if ref, ok := reg.filledOAuth[e.Name]; ok && e.OAuth == ref {
			e.OAuth = ""
		}
		// .
		// .
		if reg.filledEffort[e.Name] {
			e.EffortLevels = nil
		}
		// .
		// .
		// .
		if reg.filledAuthor[e.Name] {
			e.CatalogueAuthor = ""
		}
		if reg.filledSpeech[e.Name] {
			e.Speech = nil
		}
		ours, ok := reg.filled[e.Name]
		if !ok || len(e.CredentialOptions) == 0 {
			continue
		}
		kept := make(map[string]string, len(e.CredentialOptions))
		for k, v := range e.CredentialOptions {
			if mine, was := ours[k]; was && mine == v {
				continue
			}
			kept[k] = v
		}
		if len(kept) == 0 {
			e.CredentialOptions = nil
			continue
		}
		e.CredentialOptions = kept
	}
	return out
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
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		if _, werr := writeFileAtomic(path, scaffoldProviders()); werr != nil {
			return nil, fmt.Errorf("cannot scaffold %s: %w", path, werr)
		}
		log.Printf("No providers file found. Created default %s — user-editable, like config.json.", path)
		data, err = os.ReadFile(path)
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	// .
	// .
	// .
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

// .
// .
// .
// .
// .
// .
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
	if e.APIType != "openai" && e.APIType != "anthropic" {
		return fmt.Errorf("provider %q has unknown api_type %q (want openai or anthropic)", e.Name, e.APIType)
	}
	return nil
}

// .
func saveProvidersFile(path string, reg *providerRegistry) (bool, error) {
	data, err := json.MarshalIndent(withBrokenEntries(stripEmbeddedFills(reg)), "", "  ")
	if err != nil {
		return false, err
	}
	return writeFileAtomic(path, data)
}

// .
// .
func (a *App) setProvider(e providerEntry, keepAPIKey bool) error {
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
			// .
			// .
			if e.APIKey == "" && keepAPIKey {
				e.APIKey = reg.Providers[i].APIKey
			}
			// .
			// .
			if e.CredentialOptions == nil && e.Credential == reg.Providers[i].Credential {
				e.CredentialOptions = reg.Providers[i].CredentialOptions
				if e.OAuth == "" {
					e.OAuth = reg.Providers[i].OAuth
				}
				e.CredentialOptionFormats = reg.Providers[i].CredentialOptionFormats
			}
			// .
			// .
			// .
			// .
			// .
			// .
			if e.EffortLevels == nil {
				e.EffortLevels = reg.Providers[i].EffortLevels
			}
			// .
			// .
			if e.Speech == nil {
				e.Speech = reg.Providers[i].Speech
			}
			// .
			// .
			// .
			if e.Chat == nil {
				e.Chat = reg.Providers[i].Chat
			}
			reg.Providers[i] = e
			found = true
			break
		}
		if !found {
			reg.Providers = append(reg.Providers, e)
		}
		if e.Default {
			for i := range reg.Providers {
				if reg.Providers[i].Name != e.Name {
					reg.Providers[i].Default = false
				}
			}
		}
		return nil
	})
}

// .
// .
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
// .
func candidateRegistry(before *providerRegistry, mutate func(*providerRegistry) error) (*providerRegistry, error) {
	cp := *before
	cp.Providers = append([]providerEntry(nil), before.Providers...)
	cp.broken = slices.Clone(before.broken)
	cp.OAuth = cloneOAuth(before.OAuth)
	cp.ModelCapabilities = cloneCapabilities(before.ModelCapabilities)
	cp.DialectEffortFloor = cloneFloors(before.DialectEffortFloor)
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

// .
// .
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
		out[k] = cloneLevels(v)
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
	// .
	// .
	persist := func(reload bool) error {
		published, persistErr := saveProvidersFile(path, candidate)
		a.cfgMu.Unlock()
		if published {
			a.clearProviderStatus(name)
			if reload {
				go a.reloadConfig()
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
		// .
		// .
		// .
		// .
		// .
		return persist(false)
	}
	runtimeChanged := oldErr != nil || oldEntry.Name != nextEntry.Name ||
		!sameProviderRuntime(*oldEntry, *nextEntry, cfg.LLM.Model != "") || !a.providerRuntimeMatches(before)
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	model := cfg.LLM.Model
	if model == "" {
		model = nextEntry.DefaultModel
	}
	// .
	// .
	// .
	// .
	// .
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
	proved := a.substrateCapabilityRecord()
	if err := a.probeSubstrate(client, newCC, resolvedEntry, candidate, cfg.LLM.ProbeTimeoutSeconds); err != nil {
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
			// .
			// .
			a.setSubstrateCapability(proved)
			return fmt.Errorf("configuration changed while the provider was checked; retry")
		}
		var persistErr error
		published, persistErr = saveProvidersFile(path, candidate)
		if persistErr != nil && !published {
			return fmt.Errorf("persist providers: %w", persistErr)
		}
		if published {
			a.activateLLMRuntime(client, resolvedEntry, cfg.Prompt.MaxTokens)
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

	log.Printf("LLM: provider %q edited — validated live client activated (model %s)", name, client.ModelName())
	return nil
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
// .
// .
// .
// .
// .
// .
// .
// .
// .
type effectiveCaps struct {
	models map[string]modelCapability
	floors map[string][]string
	// .
	// .
	// .
	shippedEffort map[string]bool
}

// .
// .
func (e *effectiveCaps) effortIsShipped(model string) bool {
	return e != nil && e.shippedEffort[model]
}

var (
	embedOnce sync.Once
	embedReg  providerRegistry
)

// .
// .
// .
func embeddedRegistry() *providerRegistry {
	embedOnce.Do(func() {
		if err := json.Unmarshal(embeddedProviders, &embedReg); err != nil {
			// .
			embedReg = providerRegistry{}
		}
	})
	return &embedReg
}

// .
// .
// .
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
			// .
			if v.CacheBreakpoints == nil {
				v.CacheBreakpoints = base.CacheBreakpoints
			}
			if v.MaxCompletionTokens == nil {
				v.MaxCompletionTokens = base.MaxCompletionTokens
			}
			if v.MaxOutputTokens == 0 {
				v.MaxOutputTokens = base.MaxOutputTokens
			}
			if v.CacheRetentions == nil {
				v.CacheRetentions = base.CacheRetentions
			}
			// .
			// .
			// .
			// .
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

// .
// .
// .
// .
func (r *providerRegistry) effective() *effectiveCaps {
	if r != nil && r.eff != nil {
		return r.eff
	}
	return newEffectiveCaps(r)
}

// .
// .
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

// .
// .
func (s *effectiveCaps) dialectFloor(apiType string) ([]string, bool) {
	if s == nil {
		return nil, false
	}
	levels, ok := s.floors[apiType]
	if !ok {
		return nil, false
	}
	return cloneLevels(levels), true
}

// .
// .
// .
// .
func cloneLevels(in []string) []string {
	if in == nil {
		return nil
	}
	out := make([]string, len(in))
	copy(out, in)
	return out
}

// .
// .
// .
// .
func effortLevelsFor(eff *effectiveCaps, e providerEntry, model string) []string {
	if c, ok := eff.capabilityFor(model); ok {
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
		// .
		return c.Effort
	}
	return e.EffortLevels
}

// .
// .
// .
// .
func thinkingShapeFor(eff *effectiveCaps, e providerEntry, model string) string {
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
	c.Effort = cloneLevels(c.Effort)
	c.CacheRetentions = cloneLevels(c.CacheRetentions)
	if c.CacheBreakpoints != nil {
		v := *c.CacheBreakpoints
		c.CacheBreakpoints = &v
	}
	if c.MaxCompletionTokens != nil {
		v := *c.MaxCompletionTokens
		c.MaxCompletionTokens = &v
	}
	return c
}

// .
// .
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
			if reg.filledOAuth == nil {
				reg.filledOAuth = map[string]string{}
			}
			reg.filledOAuth[e.Name] = e.OAuth
		}
		e.signIn = oauth.Provider{}
		if e.OAuth != "" {
			p, ok := oauth.ProviderTemplate(e.OAuth, catalog)
			if !ok {
				return fmt.Errorf("provider %q names unknown OAuth contract %q", e.Name, e.OAuth)
			}
			e.signIn = p
		}
		// .
		// .
		params, err := oauth.OverrideParams(e.signIn.Params(), e.CredentialOptions)
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
