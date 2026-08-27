package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/aiii-dot-id/aii-os/config"
	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/oauth"
)

// --- Provider registry: providers.json + live model discovery ---
//
// providers.json is the provider DIRECTORY (data, never code): name, API
// type, base URL, subscribe link, default flag, and a static fallback
// model list for providers whose /models endpoint needs auth or is slow.
// The registry ships embedded; an operator's config/providers.json
// replaces it wholesale.
//
// Models are DISCOVERED on the fly from each provider's OpenAI-compatible
// GET {url}/models ({"data":[{"id":...}]}). The dashboard triggers
// discovery on page load (for the default provider) and on provider
// selection; an optional API key from the birth form is forwarded when
// the operator has typed one (localhost-only transport).

// embeddedProviders: the registry ships in-tree at config/providers.json
// (operator-editable data); the configdir package owns the embed.
var embeddedProviders = configdir.Providers

// providerEntry is one registry row (disk/JSON shape).
type providerEntry struct {
	Name      string `json:"name"`
	APIType   string `json:"api_type"` // "openai" (OpenAI-compatible, the default) | "anthropic" (native Messages API) — drives the llm client's dialect
	URL       string `json:"url"`
	APIKey    string `json:"api_key,omitempty"`     // operator's key for this provider — enter once, reused by firstboot and discovery (the file is 0600 for exactly this field)
	APIKeyEnv string `json:"api_key_env,omitempty"` // per-entry env fallback, consulted after api_key and before the global llm.api_key_env
	// Credential names a credential store to ADOPT instead of a key:
	// "claude-code" (Claude Max/Pro, ~/.claude/.credentials.json) or
	// "codex" (ChatGPT Plus/Pro, ~/.codex/auth.json). The runtime reads
	// the owner-maintained original and never writes it. A credential
	// that declares its own dialect or endpoint overrides this entry's
	// — a ChatGPT credential is only valid on the ChatGPT backend.
	Credential string `json:"credential,omitempty"`
	// CredentialOptions corrects a vendor request fact without a release:
	// file, base_url, header_<Name>, query_<name>,
	// billing_text (Anthropic Claude Code's first system block).
	CredentialOptions map[string]string `json:"credential_options,omitempty"`
	DefaultModel      string            `json:"default_model,omitempty"` // the model last chosen for this provider — preselected next time
	// Core serving parameters — operator-entered (the OpenAI-compatible
	// /models endpoint does not expose them), used to DERIVE the prompt
	// budget for the active model instead of a magic constant.
	ContextLength   int    `json:"context_length,omitempty"`    // the model window, tokens
	MaxOutputTokens int    `json:"max_output_tokens,omitempty"` // reserved for the reply
	ReasoningEffort string `json:"reasoning_effort,omitempty"`  // OpenAI-compat reasoning_effort (e.g. low|medium|high) — sent on the wire when set
	ThinkingBudget  int    `json:"thinking_budget,omitempty"`   // thinking-token budget for providers that take one (thinking_mode "budget" only)
	// ThinkingMode selects the shape of the Anthropic thinking parameter,
	// because the vendor changed it and the two shapes are mutually
	// exclusive. A vendor fact, so it lives here and not in a
	// model-name switch in Go (HITL: config.json or providers.json only).
	//   "" / "adaptive" — {"type":"adaptive"}. The current API. Required
	//                     on Opus 4.8/4.7, where OMITTING thinking turns
	//                     it off rather than leaving it at a default.
	//   "budget"        — {"type":"enabled","budget_tokens":N}. Removed
	//                     by the vendor on Opus 5/4.8/4.7, Sonnet 5 and
	//                     Fable 5, which reject it with a 400. Pre-4.6
	//                     models only.
	//   "off"           — omit the parameter entirely.
	ThinkingMode string `json:"thinking_mode,omitempty"`
	// ThinkingDisplay asks for readable reasoning: "summarized", or
	// empty for the vendor default (blocks arrive with empty text).
	ThinkingDisplay string `json:"thinking_display,omitempty"`
	// Sampling — POINTERS because 0 is a valid temperature: absent =
	// omit on the wire = server default.
	Temperature *float64 `json:"temperature,omitempty"`
	TopP        *float64 `json:"top_p,omitempty"`
	// Extra is the OpenAI-path passthrough, merged verbatim into the
	// request body top level; typed fields win on collision. Never
	// applied on the anthropic path (typed API).
	Extra        map[string]any `json:"extra,omitempty"`
	SubscribeURL string         `json:"subscribe_url,omitempty"`
	Default      bool           `json:"default,omitempty"`
	Models       []string       `json:"models,omitempty"` // static fallback when discovery fails/unauthorized
	// Local marks the entry as the operator's own serving metal (C's
	// provider.type local|remote, Go-shaped): the
	// agency.prefer_local_for_roles checkbox routes role-tagged
	// spawns here when it is alive. LAN counts — "local" means
	// operator-run, not loopback.
	Local bool `json:"local,omitempty"`
}

type providerRegistry struct {
	Providers []providerEntry `json:"providers"`

	// filled records what the EMBEDDED registry supplied at load time,
	// per provider name and option key. Unexported, so it never reaches
	// the operator's file — which is the entire point: those values are
	// vendor facts on loan for this run, not the operator's choices.
	filled map[string]map[string]string
}

// providersPath: providers.json lives beside config.json (entirely
// local — the install directory is the identity's whole world), and
// like config.json it is the operator's file: user-editable, read
// fresh on every use so live edits take effect without a restart.
func (a *App) providersPath() string {
	return providerFilePath(a.configSnapshot().SourcePath)
}

func providerFilePath(configPath string) string {
	return filepath.Join(filepath.Dir(configPath), "providers.json")
}

// loadProviders reads providers.json, SCAFFOLDING it from the embedded
// registry when absent — the file is created alongside config.json on
// first use, then the operator (and the UI's add/remove/update) own it.
// An invalid file is an ERROR, never a silent fallback (operators
// editing the registry must know it broke). 0600: entries carry API keys.

// fillEmbeddedCredentialOptions supplies vendor request facts the
// operator's file does not carry.
//
// The two files own different things. The embedded registry owns VENDOR
// FACTS — the headers Anthropic requires before it will serve a valid
// Claude Code credential, and which change when the vendor changes, on
// our release cadence. The operator's providers.json owns OPERATOR
// CHOICES: which provider, which model, their key, their ceilings.
//
// credential_options was documented as the way to "correct a vendor
// request fact without a release" — an OVERRIDE. It was also, silently,
// the only supply: a providers.json written before an option was added
// to the registry never gained it, because load scaffolds the embedded
// file only when none exists and merges nothing afterwards. A live
// identity hit exactly that — "credential requires provider options
// billing_text, header_anthropic-beta, header_user-agent, header_x-app"
// — four names whose correct values live in a file the operator has no
// reason to know about.
//
// So the registry supplies and the operator overrides, which is the
// relationship the comment always described. An operator value is never
// replaced; only absent keys are filled.
// requiredCredentialOptions returns the option names the shipped
// registry declares for a credential store.
//
// The registry IS the table. internal/oauth used to carry these four
// names as a Go slice behind a kind == KindClaudeCode special case,
// while its own comment promised the opposite: "Data, not special cases
// — a vendor that changes what it wants is a table edit." It was a
// rebuild. Now a vendor adding a fifth header is one line of
// config/providers.json, and a credential the registry says nothing
// about requires nothing.
func requiredCredentialOptions(credential string) []string {
	if credential == "" {
		return nil
	}
	var base providerRegistry
	if err := json.Unmarshal(embeddedProviders, &base); err != nil {
		return nil // a broken embed is the build's problem
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

// missingCredentialOptions names the declared options this entry does
// not actually carry. Absent keys are normally impossible after
// fillEmbeddedCredentialOptions supplies them; a blank value is an
// operator deliberately clearing one, and is still refused.
func missingCredentialOptions(credential string, opts map[string]string) []string {
	var missing []string
	for _, name := range requiredCredentialOptions(credential) {
		if strings.TrimSpace(opts[name]) == "" {
			missing = append(missing, name)
		}
	}
	return missing
}

func fillEmbeddedCredentialOptions(reg *providerRegistry) {
	var base providerRegistry
	if err := json.Unmarshal(embeddedProviders, &base); err != nil {
		return // a broken embed is the build's problem, not this load's
	}
	// Keyed by CREDENTIAL, not by provider name. The required options
	// are a property of the credential store the entry adopts —
	// oauth.go gates on exactly that — so an operator who renames their
	// Claude entry, or keeps two of them, still gets the vendor facts.
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
			continue // a plain API-key entry adopts no credential store
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
				// Remember it was ours, so saving does not hand it over.
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
}

// stripEmbeddedFills undoes fillEmbeddedCredentialOptions for writing.
//
// The fill is for USE, not for storage. Loading merged vendor facts into
// the in-memory registry and any later save serialized them into the
// operator's file — where they became operator choices, indistinguishable
// from typed ones. A shipped correction could then never replace them,
// because they now looked explicitly set. That inverts the ownership
// split this file documents: the embedded registry owns vendor request
// facts, providers.json owns operator choices.
//
// A value the operator CHANGED is theirs and stays: only a key still
// holding exactly what we put there is taken back.
func stripEmbeddedFills(reg *providerRegistry) *providerRegistry {
	if len(reg.filled) == 0 {
		return reg
	}
	out := &providerRegistry{Providers: make([]providerEntry, len(reg.Providers))}
	copy(out.Providers, reg.Providers)
	for i := range out.Providers {
		e := &out.Providers[i]
		ours, ok := reg.filled[e.Name]
		if !ok || len(e.CredentialOptions) == 0 {
			continue
		}
		kept := make(map[string]string, len(e.CredentialOptions))
		for k, v := range e.CredentialOptions {
			if mine, was := ours[k]; was && mine == v {
				continue // still exactly what we loaned it
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

func (a *App) loadProviders() (*providerRegistry, error) {
	return loadProvidersFile(a.providersPath())
}

func loadProvidersFile(path string) (*providerRegistry, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		if _, werr := writeFileAtomic(path, embeddedProviders); werr != nil {
			return nil, fmt.Errorf("cannot scaffold %s: %w", path, werr)
		}
		log.Printf("No providers file found. Created default %s — user-editable, like config.json.", path)
		data, err = os.ReadFile(path)
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var reg providerRegistry
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&reg); err != nil {
		return nil, fmt.Errorf("%s is invalid: %w", path, err)
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return nil, fmt.Errorf("%s is invalid: trailing JSON data", path)
	}
	fillEmbeddedCredentialOptions(&reg)

	names := make(map[string]struct{}, len(reg.Providers))
	defaultName := ""
	for i := range reg.Providers {
		e := &reg.Providers[i]
		if e.Name == "" {
			return nil, fmt.Errorf("%s is invalid: provider name is empty", path)
		}
		if _, exists := names[e.Name]; exists {
			return nil, fmt.Errorf("%s is invalid: duplicate provider %q", path, e.Name)
		}
		names[e.Name] = struct{}{}
		if e.Default {
			if defaultName != "" {
				return nil, fmt.Errorf("%s is invalid: providers %q and %q are both default", path, defaultName, e.Name)
			}
			defaultName = e.Name
		}
		if err := normalizeProviderAPIType(e); err != nil {
			return nil, fmt.Errorf("%s is invalid: %w", path, err)
		}
	}
	return &reg, nil
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

// saveProviders writes the registry back, atomically, 0600.
func saveProvidersFile(path string, reg *providerRegistry) (bool, error) {
	data, err := json.MarshalIndent(stripEmbeddedFills(reg), "", "  ")
	if err != nil {
		return false, err
	}
	return writeFileAtomic(path, data)
}

// setProvider upserts one entry by name (the UI's add and update are the
// same act). A set default clears every other default — one default.
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
			// The browser never receives the secret. Its has_key bit makes
			// blank explicit: keep when true, clear when false.
			if e.APIKey == "" && keepAPIKey {
				e.APIKey = reg.Providers[i].APIKey
			}
			// Credential options are file-only deployment data, outside the
			// dashboard update surface.
			if e.CredentialOptions == nil && e.Credential == reg.Providers[i].Credential {
				e.CredentialOptions = reg.Providers[i].CredentialOptions
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

// deleteProvider removes one entry by name. An empty registry is the
// operator's legal choice — the firstboot card simply offers nothing.
func (a *App) deleteProvider(name string) error {
	return a.changeProviders(name, func(reg *providerRegistry) error {
		kept := reg.Providers[:0]
		for _, e := range reg.Providers {
			if e.Name != name {
				kept = append(kept, e)
			}
		}
		if len(kept) == len(reg.Providers) {
			return fmt.Errorf("no provider named %q", name)
		}
		reg.Providers = kept
		return nil
	})
}

// changeProviders is the one in-process load-modify-save boundary. If the
// edit changes the resolved live substrate, the same use-door as a pointer
// switch must pass before the file or client moves.
func (a *App) changeProviders(name string, mutate func(*providerRegistry) error) error {
	path := a.providersPath()
	a.cfgMu.Lock()
	before, err := loadProvidersFile(path)
	if err != nil {
		a.cfgMu.Unlock()
		return err
	}
	candidate := &providerRegistry{Providers: append([]providerEntry(nil), before.Providers...)}
	if err := mutate(candidate); err != nil {
		a.cfgMu.Unlock()
		return err
	}
	if reflect.DeepEqual(before, candidate) {
		a.cfgMu.Unlock()
		return nil
	}
	cfg := *a.cfg
	if a.llmSwap == nil {
		published, persistErr := saveProvidersFile(path, candidate)
		a.cfgMu.Unlock()
		if published {
			a.clearProviderStatus(name)
		}
		if persistErr != nil {
			if published {
				return fmt.Errorf("providers were published but directory durability is unconfirmed: %w", persistErr)
			}
			return fmt.Errorf("persist providers: %w", persistErr)
		}
		return nil
	}

	oldEntry, oldErr := selectProvider(cfg.LLM, before)
	nextEntry, newErr := selectProvider(cfg.LLM, candidate)
	if newErr != nil {
		a.cfgMu.Unlock()
		return fmt.Errorf("provider change refused: %w; current substrate kept", newErr)
	}
	runtimeChanged := oldErr != nil || oldEntry.Name != nextEntry.Name ||
		!sameProviderRuntime(*oldEntry, *nextEntry, cfg.LLM.Model != "")
	if !runtimeChanged {
		published, persistErr := saveProvidersFile(path, candidate)
		a.cfgMu.Unlock()
		if published {
			a.clearProviderStatus(name)
		}
		if persistErr != nil {
			if published {
				return fmt.Errorf("providers were published but directory durability is unconfirmed: %w", persistErr)
			}
			return fmt.Errorf("persist providers: %w", persistErr)
		}
		return nil
	}
	a.cfgMu.Unlock()

	newCC, resolvedEntry, err := a.resolveLLMConfig(cfg.LLM, candidate)
	if err != nil {
		return fmt.Errorf("provider change refused: %w; current substrate kept", err)
	}
	client := a.newLLMClient(newCC, promptBudgetFor(resolvedEntry, cfg.Prompt.MaxTokens))
	if err := a.probeSubstrate(client, newCC, resolvedEntry); err != nil {
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

func sameProviderRuntime(a, b providerEntry, modelPinned bool) bool {
	a.SubscribeURL, b.SubscribeURL = "", ""
	a.Default, b.Default = false, false
	a.Models, b.Models = nil, nil
	if modelPinned {
		a.DefaultModel, b.DefaultModel = "", ""
	}
	return reflect.DeepEqual(a, b)
}

func (a *App) clearProviderStatus(name string) {
	a.provMu.Lock()
	delete(a.provStatus, name)
	a.provMu.Unlock()
}

// --- The pointer resolved (operator ruling 2026-08-20) ---
//
// config.json's llm block is a POINTER: provider (a providers.json entry
// name; empty = the default-flagged entry) and model (empty = the
// entry's default_model), plus transport knobs that belong to no
// provider. Provider DATA — endpoint, key, window, budgets, effort —
// lives ONLY on the registry entry. resolveLLM is the one place the
// pointer dereferences; everything that dials a substrate goes through
// it, so an edit to providers.json travels by reference instead of
// being copied into config and drifting.

// resolveLLM dereferences cfg.LLM against providers.json and returns
// the ready-to-dial client config plus the resolved entry (context-
// length consumers read the entry). The api key falls back to
// $llm.api_key_env when the entry stores none; a local provider may need
// no key, while a remote provider reports its own refusal at call time.
// The visible-output floor clamps LOUDLY here on the resolved values
// (an identity that thinks forever and says nothing is a config trap;
// the clamp lived in applyDefaults while the budgets were config
// fields — they are entry data now, so the resolver disarms it).
func (a *App) resolveLLM() (llm.ClientConfig, providerEntry, error) {
	cfg := a.configSnapshot()
	reg, err := a.loadProviders()
	if err != nil {
		return llm.ClientConfig{}, providerEntry{}, err
	}
	return a.resolveLLMConfig(cfg.LLM, reg)
}

func (a *App) resolveLLMConfig(cfg LLMConfig, reg *providerRegistry) (llm.ClientConfig, providerEntry, error) {
	entry, err := selectProvider(cfg, reg)
	if err != nil {
		return llm.ClientConfig{}, providerEntry{}, err
	}
	model := cfg.Model
	if model == "" {
		model = entry.DefaultModel
	}
	if model == "" {
		return llm.ClientConfig{}, providerEntry{}, fmt.Errorf("no model: set llm.model or a default_model on provider %q", entry.Name)
	}
	apiKey := providerAPIKey(*entry, "", cfg.APIKeyEnv)
	resolved := *entry
	// The window is the provider's fact. Use what it published unless the
	// operator typed one — an override beats a guess, and a guess beats
	// nothing, but a published truth beats both.
	if resolved.ContextLength == 0 || resolved.MaxOutputTokens == 0 {
		if m, ok := a.discoveredMeta(entry.Name, model); ok {
			if resolved.ContextLength == 0 {
				resolved.ContextLength = m.Context
			}
			if resolved.MaxOutputTokens == 0 {
				resolved.MaxOutputTokens = m.MaxOut
			}
		}
	}
	if err := validateModelWindow(resolved, model); err != nil {
		return llm.ClientConfig{}, providerEntry{}, err
	}
	resolved = resolveOutputAllocation(resolved)

	// An adopted credential supplies the token AND, where the vendor
	// scopes it, the dialect and the endpoint it is valid against. A
	// ChatGPT credential on an entry pointing at api.openai.com would 403
	// all day; the credential knows better than the entry, so it wins.
	cc, terr := a.clientConfigForEntry(resolved, cfg, model, apiKey)
	if terr != nil {
		return llm.ClientConfig{}, providerEntry{}, terr
	}
	return cc, resolved, nil
}

const (
	// defaultOutputReserve is llm's number, not a second opinion about
	// it. The prompt budget reserves what the wire will actually send.
	defaultOutputReserve = llm.DefaultMaxOutputTokens
	promptSafetyTokens   = 2048
)

func validateModelWindow(entry providerEntry, model string) error {
	if entry.ContextLength < 0 || entry.MaxOutputTokens < 0 || entry.ThinkingBudget < 0 {
		return fmt.Errorf("provider %q model %q has negative token limits", entry.Name, model)
	}
	if entry.ContextLength == 0 {
		return nil
	}
	reserve := entry.MaxOutputTokens
	if reserve == 0 {
		reserve = defaultOutputReserve
	}
	if entry.ContextLength <= reserve+promptSafetyTokens {
		return fmt.Errorf("provider %q model %q context_length %d must exceed output reserve %d plus safety margin %d",
			entry.Name, model, entry.ContextLength, reserve, promptSafetyTokens)
	}
	return nil
}

// resolveOutputAllocation settles how many output tokens this entry gets
// and WRITES IT BACK, then keeps a floor of visible output beneath any
// thinking budget.
//
// The write-back is the point. The fallback used to be applied locally
// wherever someone needed a number: the prompt budget reserved 8192, and
// the entry still carried zero — so the OpenAI-shaped dialects omitted
// max_tokens entirely and let the provider choose, while the Anthropic
// dialect substituted its own private 8192. AII OS budgeted against a
// ceiling that only one of three dialects was actually sending.
// Materialized here, one number reaches the prompt budget, the client
// config and every wire.
func resolveOutputAllocation(entry providerEntry) providerEntry {
	const visibleFloor = 1024
	if entry.MaxOutputTokens <= 0 {
		entry.MaxOutputTokens = defaultOutputReserve
	}
	output := entry.MaxOutputTokens
	if entry.ThinkingBudget <= 0 || output-entry.ThinkingBudget >= visibleFloor {
		return entry
	}
	requested := entry.ThinkingBudget
	entry.ThinkingBudget = output - visibleFloor
	if entry.ThinkingBudget < 0 {
		entry.ThinkingBudget = 0
	}
	log.Printf("LLM: provider %q thinking budget clamped %d -> %d — the entry left < %d visible output tokens of %d",
		entry.Name, requested, entry.ThinkingBudget, visibleFloor, output)
	return entry
}

// providerAPIKey resolves the credential for ONE provider.
//
// A PROVIDER THAT NAMES ITS KEY SOURCE HAS ANSWERED THE QUESTION, and
// empty is one of the answers. It previously fell through to the global
// llm.api_key_env when its own variable was unset — and that global
// DEFAULTS to "OPENAI_API_KEY" whether or not the operator ever set it
// (config.go). So selecting a provider whose ANTHROPIC_API_KEY happened
// to be missing sent the OpenAI key to api.anthropic.com: a credential
// handed to a third party, over a mistake as ordinary as an unexported
// variable.
//
// The fallback survives only for an entry that names NO source at all,
// which is what llm.api_key_env means — the operator's answer for
// providers that did not give one. Reported by ChatGPT Sol 5.6
// (2026-08-24) as Critical; confirmed exactly as written.
func providerAPIKey(entry providerEntry, supplied, fallbackEnv string) string {
	if supplied != "" {
		return supplied
	}
	if entry.APIKey != "" {
		return entry.APIKey
	}
	if entry.APIKeyEnv != "" {
		key := os.Getenv(entry.APIKeyEnv)
		if key == "" {
			// Loud, because the alternative failure is a 401 from an
			// endpoint the operator did not think they were debugging.
			log.Printf("LLM: provider %q names %s for its key and that variable is empty — "+
				"sending no credential rather than another provider's",
				entry.Name, entry.APIKeyEnv)
		}
		return key
	}
	return os.Getenv(fallbackEnv)
}

func selectProvider(cfg LLMConfig, reg *providerRegistry) (*providerEntry, error) {
	if name := cfg.Provider; name != "" {
		for i := range reg.Providers {
			if reg.Providers[i].Name == name {
				return &reg.Providers[i], nil
			}
		}
		return nil, fmt.Errorf("llm.provider %q is not in providers.json (%d providers)", name, len(reg.Providers))
	}
	for i := range reg.Providers {
		if reg.Providers[i].Default {
			return &reg.Providers[i], nil
		}
	}
	return nil, fmt.Errorf("llm.provider is empty and providers.json flags no default provider")
}

// promptBudgetFor derives the runtime prompt ceiling from the resolved
// provider entry. The operator's prompt.max_tokens is a ceiling; model
// metadata may narrow it but never widen it.
func promptBudgetFor(entry providerEntry, promptBudget int) int {
	if cl := entry.ContextLength; cl > 0 {
		reserve := entry.MaxOutputTokens
		if reserve <= 0 {
			reserve = defaultOutputReserve
		}
		if derived := cl - reserve - promptSafetyTokens; derived > 0 && (promptBudget == 0 || derived < promptBudget) {
			log.Printf("Prompt budget derived from model window: %d (context %d - output %d - margin %d)", derived, cl, reserve, promptSafetyTokens)
			promptBudget = derived
		}
	}
	if promptBudget == 0 {
		promptBudget = 32000
		// SAY WHICH NUMBER IS IN CHARGE (the witness-floor lesson,
		// 2026-08-26, applied to the prompt path): this fallback ran a
		// 200K-context model on a 32K window for weeks, folding the
		// exact bytes the model had just read — silently.
		log.Printf("Prompt budget: FALLBACK %d tokens — provider %q declares no context_length and none was discovered; the model's real window may be far larger. Set context_length on the provider entry (Settings → Providers).",
			promptBudget, entry.Name)
	}
	return promptBudget
}

// activateLLMRuntime moves every consumer of resolved model data before the
// next turn begins. Callers serialize this boundary through turnGate.
func (a *App) activateLLMRuntime(client *llm.Client, entry providerEntry, maxPromptTokens int) {
	promptBudget := promptBudgetFor(entry, maxPromptTokens)
	if a.composer != nil {
		a.composer.SetMaxTokens(promptBudget)
	}
	if a.promptGate != nil {
		a.promptGate.SetMaxTokens(promptBudget)
	}
	if a.conv != nil {
		a.conv.SetModelLimits(promptBudget, entry.ThinkingBudget)
	}
	if a.ledger != nil {
		a.ledger.SetModelID(client.ModelName())
	}
	if a.llmSwap != nil {
		a.llmSwap.Swap(client)
	}
}

var (
	// errAuthRequired marks a provider whose /models needs credentials the
	// caller doesn't have — the form guides the operator, not a raw 401.
	errAuthRequired = errors.New("this provider requires an API key to list models")
	// errCredentialUnavailable marks failure before provider dispatch while
	// preserving the credential owner's actionable error.
	errCredentialUnavailable = errors.New("provider credential unavailable")
)

const maxModelListBytes = 1 << 20

// discoverModels lists a provider's models. apiKey may be empty; an
// unauthenticated 401 is classified as errAuthRequired so callers can guide
// the operator. The path and auth header
// are DIALECT properties, not universal ones: an OpenAI-compatible base
// serves /models and takes a bearer, while the native Anthropic base
// serves /v1/models (its chat path supplies its own /v1) and takes
// x-api-key — or a bearer when the credential is an adopted OAuth token.
// Assuming one shape for all of them is what made api_type "anthropic"
// unselectable: the probe asked api.anthropic.com/models, a hard 404.
func discoverModels(ctx context.Context, url, apiKey string) ([]string, error) {
	models, _, err := discoverModelsWith(ctx, "", url, apiKey, false, nil, nil)
	return models, err
}

func discoverModelsWith(ctx context.Context, dialect, base, token string, bearer bool, extra, query map[string]string) ([]string, map[string]modelMeta, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	base = strings.TrimSuffix(base, "/")
	// WHERE the list lives is a property of the URL, not of a dialect
	// label: if the base already carries an API version the list hangs
	// directly off it, otherwise the version belongs in the path. That
	// one rule covers an OpenAI base with or without /v1, and an
	// Anthropic base with or without it — the case that broke us before.
	path := base + "/v1/models"
	if apiVersionInPath(base) {
		path = base + "/models"
	}
	if dialect == "chatgpt" {
		path = base + "/models" // a product backend, not a versioned API
	}
	anthropic := dialect == "anthropic"

	if len(query) > 0 {
		q := url.Values{}
		for k, v := range query {
			q.Set(k, v)
		}
		path += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, "GET", path, nil)
	if err != nil {
		return nil, nil, err
	}
	if token != "" {
		if anthropic && !bearer {
			req.Header.Set("x-api-key", token)
		} else {
			req.Header.Set("Authorization", "Bearer "+token)
		}
	}
	if anthropic {
		if req.Header.Get("anthropic-version") == "" {
			req.Header.Set("anthropic-version", llm.DefaultAnthropicVersion)
		}
	}
	for k, v := range extra {
		req.Header.Set(k, v)
	}

	resp, err := httpDiscoveryClient.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		// Guidance, not a raw status: with no credential the operator needs
		// to enter one; with a credential the credential is the problem.
		if token == "" {
			return nil, nil, errAuthRequired
		}
		return nil, nil, fmt.Errorf("key rejected (%d) — check the credential for this provider", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("provider returned %d listing models at %s", resp.StatusCode, path)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxModelListBytes+1))
	if err != nil {
		return nil, nil, err
	}
	if len(body) > maxModelListBytes {
		return nil, nil, fmt.Errorf("provider model list exceeds %d bytes", maxModelListBytes)
	}
	return parseModelList(body)
}

// parseModelList reads the model-list shapes that actually exist rather
// than the one the OpenAI spec describes: {"data":[{"id"}]} from
// OpenAI-compatible bases and Anthropic, and {"models":[{"slug"}]} from
// the ChatGPT backend. A local runner's {"models":[{"name"}]} falls out
// of the same branch. Three real shapes earn a tolerant reader; one
// would not have.
func parseModelList(body []byte) ([]string, map[string]modelMeta, error) {
	var out struct {
		Data []struct {
			ID            string `json:"id"`
			MaxInput      int    `json:"max_input_tokens"` // Anthropic
			MaxTokens     int    `json:"max_tokens"`
			ContextLength int    `json:"context_length"` // OpenRouter-style
		} `json:"data"`
		Models []struct {
			Slug          string `json:"slug"`
			Name          string `json:"name"`
			ID            string `json:"id"`
			ContextWindow int    `json:"context_window"` // ChatGPT backend
			MaxTokens     int    `json:"max_tokens"`
		} `json:"models"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, nil, fmt.Errorf("provider model list is not JSON: %w", err)
	}
	models := make([]string, 0, len(out.Data)+len(out.Models))
	meta := map[string]modelMeta{}
	for _, m := range out.Data {
		if m.ID == "" {
			continue
		}
		models = append(models, m.ID)
		ctx := m.MaxInput
		if ctx == 0 {
			ctx = m.ContextLength
		}
		if ctx > 0 || m.MaxTokens > 0 {
			meta[m.ID] = modelMeta{Context: ctx, MaxOut: m.MaxTokens}
		}
	}
	for _, m := range out.Models {
		id := m.Slug
		if id == "" {
			id = m.ID
		}
		if id == "" {
			id = m.Name
		}
		if id == "" {
			continue
		}
		models = append(models, id)
		if m.ContextWindow > 0 || m.MaxTokens > 0 {
			meta[id] = modelMeta{Context: m.ContextWindow, MaxOut: m.MaxTokens}
		}
	}
	return models, meta, nil
}

// modelMeta is what a provider says about one model. DISCOVERED, never
// stored: the window is the provider's fact, and guessing it silently
// mis-budgets every prompt. A value the operator typed on the entry still
// wins — that is an override, not a guess.
type modelMeta struct {
	Context int
	MaxOut  int
}

// httpDiscoveryClient bounds discovery independently of the default
// client (which has no timeout and follows redirects anywhere).
var httpDiscoveryClient = &http.Client{
	Timeout: 15 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

// discoverForEntry probes one provider the way that provider must be
// probed, adopting its credential when it has one.
func (a *App) discoverForEntry(ctx context.Context, e providerEntry, apiKey string) ([]string, error) {
	models, _, err := a.discoverMetaForEntry(ctx, e, apiKey)
	return models, err
}

func (a *App) discoverMetaForEntry(ctx context.Context, e providerEntry, apiKey string) ([]string, map[string]modelMeta, error) {
	fallbackEnv := ""
	if a.cfg != nil {
		fallbackEnv = a.configSnapshot().LLM.APIKeyEnv
	}
	apiKey = providerAPIKey(e, apiKey, fallbackEnv)
	dialect, base, bearer := e.APIType, e.URL, false
	var extra, query map[string]string
	if e.Credential != "" {
		src, err := a.credentialSource(e.Credential, e.CredentialOptions)
		if err != nil {
			return nil, nil, fmt.Errorf("%w: %w", errCredentialUnavailable, err)
		}
		cr, err := src.Credential(ctx)
		if err != nil {
			return nil, nil, fmt.Errorf("%w: %w", errCredentialUnavailable, err)
		}
		apiKey, bearer, extra, query = cr.Token, true, cr.Headers, src.DiscoveryQuery()
		if b := src.BaseURL(); b != "" {
			base = b
		}
		if d := src.Dialect(); d != "" {
			dialect = d
		}
	}
	return discoverModelsWith(ctx, dialect, base, apiKey, bearer, extra, query)
}

// discoverForProvider resolves a registry row by name and discovers its
// models, falling back to the static list on any failure (the form must
// always be usable; discovery is enhancement).
func (a *App) discoverForProvider(ctx context.Context, reg *providerRegistry, name, apiKey string) ([]string, error) {
	for _, p := range reg.Providers {
		if p.Name == name {
			models, err := a.discoverForEntry(ctx, p, apiKey)
			if err == nil {
				return mergeModels(p.Models, models), nil
			}
			if len(p.Models) > 0 {
				return mergeModels(p.Models, nil), nil // static fallback, form stays usable
			}
			return nil, err
		}
	}
	return nil, fmt.Errorf("unknown provider %q", name)
}

func mergeModels(lists ...[]string) []string {
	seen := make(map[string]bool)
	var models []string
	for _, list := range lists {
		for _, model := range list {
			if model != "" && !seen[model] {
				models = append(models, model)
				seen[model] = true
			}
		}
	}
	return models
}

// setProviderInfo adapts the dashboard shape to the registry entry —
// the UI's add and update, one act.
func (a *App) setProviderInfo(in dashboard.ProviderInfo) error {
	return a.setProvider(providerEntry{
		Name: in.Name, APIType: in.APIType, URL: in.Endpoint,
		APIKey: in.APIKey, APIKeyEnv: in.APIKeyEnv, Credential: in.Credential,
		DefaultModel: in.DefaultModel, SubscribeURL: in.SubscribeURL,
		ContextLength: in.ContextLength, MaxOutputTokens: in.MaxOutputTokens,
		ReasoningEffort: in.ReasoningEffort, ThinkingBudget: in.ThinkingBudget, ThinkingMode: in.ThinkingMode, ThinkingDisplay: in.ThinkingDisplay,
		Temperature: in.Temperature, TopP: in.TopP, Extra: in.Extra,
		Default: in.Default, Models: in.ConfiguredModels,
	}, in.APIKey == "" && in.HasKey)
}

// --- Derived provider status (design pass 2026-08-20) ---
//
// Reachability is a fact about NOW, never a stored property: a provider
// is probed via the ONE validator (discoverModels) and the result lives
// only in this in-memory cache. TTL is a constant, not a knob. The
// management list shows every entry with its status so a red row can be
// fixed; activation still requires real inference.

const providerStatusTTL = 60 * time.Second

type providerProbe struct {
	state     string // "ok" | "auth_required" | "no_credential" | "unreachable" | "invalid_url"
	reason    string // why, when the state alone does not say it
	models    []string
	meta      map[string]modelMeta // what the provider says about each model
	checkedAt time.Time
	key       string // endpoint it validated; dashboard edits clear the entry
}

func probeKey(e providerEntry) string {
	data, _ := json.Marshal(e) // provider rows came from JSON and are marshalable
	hash := sha256.Sum256(data)
	return string(hash[:])
}

// probeProviders fills current statuses for every entry, probing stale
// ones in parallel (bounded by the per-call timeout). Caller gets a map
// keyed by provider name.
func (a *App) probeProviders(reg *providerRegistry) map[string]providerProbe {
	now := time.Now()
	a.provMu.Lock()
	if a.provStatus == nil {
		a.provStatus = make(map[string]providerProbe)
	}
	var stale []providerEntry
	out := make(map[string]providerProbe, len(reg.Providers))
	for _, e := range reg.Providers {
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
			a.provStatus[r.name] = r.st
			out[r.name] = r.st
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
	// Probe THROUGH the entry, so a credential-backed provider is checked
	// with its credential instead of reported as needing a key it does
	// not use.
	models, meta, err := a.discoverMetaForEntry(ctx, e, e.APIKey)
	switch {
	case err == nil:
		st.state = "ok"
		st.models, st.meta = models, meta
	case err == errAuthRequired:
		st.state = "auth_required"
	case e.Credential != "" && errors.Is(err, errCredentialUnavailable):
		// The provider is fine; the CREDENTIAL is not here. Reporting
		// that as "unreachable" reads as though Anthropic were down, and
		// tells the operator nothing they can act on.
		st.state = "no_credential"
		st.reason = err.Error()
	default:
		st.state = "unreachable"
		st.reason = err.Error()
	}
	return st
}

// validProviderURL is the ENTRY-fact check (refused on save): parseable,
// http(s), with a host. Reachability is a NOW-fact and never refuses a
// save — it flags.
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

// providerDirectoryLive is the listing the UI consumes: every entry,
// with status and one model list. Configured models remain usable;
// successful discovery adds models the endpoint reports.
func (a *App) providerDirectoryLive() []dashboard.ProviderInfo {
	reg, err := a.loadProviders()
	if err != nil {
		log.Printf("providers: %v", err)
		return nil
	}
	probes := a.probeProviders(reg)
	out := make([]dashboard.ProviderInfo, 0, len(reg.Providers))
	for _, e := range reg.Providers {
		pr := probes[e.Name]
		models := mergeModels(e.Models)
		if pr.state == "ok" {
			models = mergeModels(models, pr.models)
		}
		if e.DefaultModel != "" {
			models = mergeModels([]string{e.DefaultModel}, models)
		}
		out = append(out, dashboard.ProviderInfo{
			Name: e.Name, APIType: e.APIType, Endpoint: e.URL,
			HasKey: e.APIKey != "", APIKeyEnv: e.APIKeyEnv, Credential: e.Credential,
			CredentialInfo: a.credentialInfo(e),
			DefaultModel:   e.DefaultModel, SubscribeURL: e.SubscribeURL,
			ContextLength: e.ContextLength, MaxOutputTokens: e.MaxOutputTokens,
			ReasoningEffort: e.ReasoningEffort, ThinkingBudget: e.ThinkingBudget, ThinkingMode: e.ThinkingMode, ThinkingDisplay: e.ThinkingDisplay,
			Temperature: e.Temperature, TopP: e.TopP, Extra: e.Extra,
			Default: e.Default, Models: models, ConfiguredModels: e.Models, Status: pr.state, StatusReason: pr.reason,
		})
	}
	markPreselect(out)
	return out
}

// oauthAdapter presents an adopted credential store to the llm client.
type oauthAdapter struct{ s *oauth.Source }

func (a oauthAdapter) Credential(ctx context.Context) (llm.Credential, error) {
	c, err := a.s.Credential(ctx)
	if err != nil {
		return llm.Credential{}, err
	}
	return llm.Credential{Token: c.Token, Headers: c.Headers, Gen: c.Gen}, nil
}

func (a oauthAdapter) Stale(ctx context.Context, gen uint64) error { return a.s.Stale(ctx, gen) }

// entryTransport resolves how to REACH one provider. An adopted credential
// supplies its endpoint, dialect, authorization, and request metadata and
// outranks the entry, because it records what will actually work.
// Every caller goes through here — including birth, which used to build
// a four-field client by hand and could therefore neither adopt a
// subscription nor speak a non-OpenAI dialect.
func (a *App) entryTransport(e providerEntry, apiKey string) (llm.ClientConfig, error) {
	cc := llm.ClientConfig{Endpoint: e.URL, Provider: e.APIType, APIKey: apiKey}
	if e.Credential == "" {
		return cc, nil
	}
	src, cerr := a.credentialSource(e.Credential, e.CredentialOptions)
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

func (a *App) clientConfigForEntry(e providerEntry, cfg LLMConfig, model, apiKey string) (llm.ClientConfig, error) {
	cc, err := a.entryTransport(e, providerAPIKey(e, apiKey, cfg.APIKeyEnv))
	if err != nil {
		return llm.ClientConfig{}, err
	}
	cc.Model = model
	cc.ThinkingBudget = e.ThinkingBudget
	// The registry owns which thinking shape the vendor accepts and
	// whether readable reasoning is wanted. Without these two lines the
	// fields existed everywhere and reached the wire nowhere.
	cc.ThinkingMode = e.ThinkingMode
	cc.ThinkingDisplay = e.ThinkingDisplay
	cc.ReasoningEffort = e.ReasoningEffort
	cc.MaxOutputTokens = e.MaxOutputTokens
	cc.Temperature = e.Temperature
	cc.TopP = e.TopP
	cc.Extra = e.Extra
	cc.TimeoutSeconds = cfg.TimeoutSeconds
	cc.Retries = cfg.Retries
	cc.RetryBackoffMS = cfg.RetryBackoffMS
	return cc, nil
}

// birthEntry is the provider the operator is birthing on: the stored
// entry when the form names one (so its credential and dialect apply),
// with whatever they typed taking precedence.
func (a *App) birthEntry(name, endpoint, model string) (providerEntry, error) {
	e := providerEntry{Name: name, URL: endpoint, DefaultModel: model}
	if name == "" {
		return e, nil
	}
	reg, err := a.loadProviders()
	if err != nil {
		return providerEntry{}, err
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
		return e, nil
	}
	return e, nil
}

// apiVersionInPath reports whether a base URL already names an API
// version (/v1, /v2, /v1beta …).
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

// discoveredMeta reads what the last probe learned about a model. Cache
// only — never a network call on the resolve path.
func (a *App) discoveredMeta(provider, model string) (modelMeta, bool) {
	a.provMu.Lock()
	defer a.provMu.Unlock()
	pr, ok := a.provStatus[provider]
	if !ok || pr.meta == nil {
		return modelMeta{}, false
	}
	m, ok := pr.meta[model]
	return m, ok
}

// markPreselect chooses what the birth form should open on: a provider
// whose adopted credential is present and WORKING, in registry order, so
// an operator holding a subscription births with nothing to paste.
// Registry order puts Anthropic before OpenAI deliberately — the first
// is the public documented API on its native dialect, the second a
// private backend. Falls back to the registry default. Derived per
// request, never stored: a lapsed subscription simply stops being it.
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

// credentialInfo describes an entry's adopted credential for the operator
// surface. Best-effort: a store that cannot be read reports the reason,
// which is more useful than an empty field.
func (a *App) credentialInfo(e providerEntry) *dashboard.CredentialInfo {
	if e.Credential == "" {
		return nil
	}
	src, err := a.credentialSource(e.Credential, e.CredentialOptions)
	if err != nil {
		return &dashboard.CredentialInfo{Kind: e.Credential, Error: err.Error()}
	}
	i := src.Info()
	out := &dashboard.CredentialInfo{
		Kind: i.Kind, Plan: i.Plan, Tier: i.Tier, IsAPIKey: i.IsAPIKey, Path: i.Path,
	}
	if !i.ExpiresAt.IsZero() {
		// Publish the USABLE boundary, not the vendor's nominal expiry.
		// The runtime stops accepting the credential a skew early, and an
		// operator told the later time has no way to know why their
		// identity stopped thinking fifteen minutes before the clock said
		// it should.
		usable := i.ExpiresAt.Add(-oauth.ExpirySkew)
		out.ExpiresAt = usable.UTC().Format(time.RFC3339)
		out.Expired = !time.Now().Before(usable)
	}
	return out
}

// credentialWarnWindow is how long before a credential becomes unusable
// the operator is told. Thirty minutes is chosen to be longer than any
// plausible turn and long enough to run the owner's own tool without
// hurrying.
const credentialWarnWindow = 30 * time.Minute

// credentialWarning is what the operator sees before the lights go out.
//
// The runtime may never refresh an adopted credential — rotation would
// invalidate the owner's copy and lock them out of their own tool — so
// the only remedy is a human running that tool. Until now nothing asked
// them to: the identity worked, then abruptly did not, with an error it
// could only produce for the operator who happened to be typing at that
// moment. Everything needed to see it coming was already known.
//
// Empty in the ordinary case, which is nearly always.
func (a *App) credentialWarning() string {
	_, entry, err := a.resolveLLM()
	if err != nil || entry.Credential == "" {
		return "" // an API key does not expire out from under us
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

// credentialWarningFor is the whole decision, separated from the
// provider plumbing so the arithmetic can be tested at the boundary
// rather than inferred from a live credential file.
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
