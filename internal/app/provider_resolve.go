package app

import (
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"

	"github.com/aiii-dot-id/aii-os/internal/llm"
)

type providerState struct {
	runtime providerRuntime
	custody credentialCustody
	sources credentialSources
}

type providerRuntime struct {
	mu sync.RWMutex

	activeProvider providerEntry

	activeClient *llm.Client

	credentialReplaced bool

	signIns uint64

	activeBudget       int
	activeBudgetSource budgetSource

	activeFacilityBudget       int
	activeFacilityBudgetSource budgetSource

	substrateCap substrateCapability

	provStatus map[string]providerProbe
}

func sameProviderRuntime(a, b providerEntry, modelPinned bool) bool {
	a.SubscribeURL, b.SubscribeURL = "", ""
	a.Default, b.Default = false, false
	a.Models, b.Models = nil, nil
	a.Chat, b.Chat = nil, nil
	if modelPinned {
		a.DefaultModel, b.DefaultModel = "", ""
	}
	a.signInsSeen, b.signInsSeen = 0, 0
	return reflect.DeepEqual(a, b)
}

func (a *App) clearProviderStatus(name string) {
	a.providers.runtime.mu.Lock()
	delete(a.providers.runtime.provStatus, name)
	a.providers.runtime.mu.Unlock()
}

func (a *App) retainProviderStatusFor(entry providerEntry) {
	a.providers.runtime.mu.Lock()
	defer a.providers.runtime.mu.Unlock()
	if a.providers.runtime.provStatus[entry.Name].key != probeKey(entry) {
		delete(a.providers.runtime.provStatus, entry.Name)
	}
}

func (a *App) resolveLLM() (llm.ClientConfig, providerEntry, error) {
	cfg := a.configSnapshot()
	reg, err := a.loadProviders()
	if err != nil {
		return llm.ClientConfig{}, providerEntry{}, err
	}
	cc, entry, err := a.resolveLLMConfig(cfg.LLM, reg)
	if err == nil {

		if note := reg.movedRouteNote(entry.Name); note != "" {
			logsink.Info("providers.start", "%s", note)
		}
	}
	return cc, entry, err
}

func (a *App) resolveLLMConfig(cfg LLMConfig, reg *providerRegistry) (llm.ClientConfig, providerEntry, error) {

	a.providers.runtime.mu.RLock()
	signIns := a.providers.runtime.signIns
	a.providers.runtime.mu.RUnlock()
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
	resolved.signInsSeen = signIns

	if resolved.ContextLength == 0 || resolved.MaxOutputTokens == 0 {
		m, found, listed, named := a.discoveredMeta(entry.Name, model)

		windowless := resolved.ContextLength == 0 && limitModelWindow(resolved, model, reg.effective()).ContextLength == 0
		switch {
		case found:
			window, reserve := discoveredWindow(m, resolved, entry.Name, model)
			if resolved.ContextLength == 0 {
				resolved.ContextLength = window
			}
			if resolved.MaxOutputTokens == 0 {
				resolved.MaxOutputTokens = reserve
			}
		case windowless && named:

			logsink.Warn("llm.refusal", "provider %q lists %q but publishes no context window for it — the prompt budget cannot be derived from the model's window; set context_length on the entry",
				entry.Name, model)
		case windowless && listed > 0:

			logsink.Warn("llm.refusal", "provider %q lists %d model(s) and none is named %q (by id or canonical name) — its window cannot be derived from the list; set context_length on the entry or use a name the provider lists",
				entry.Name, listed, model)
		}
	}
	resolved = limitToModel(resolved, model, reg.effective())
	if err := validateModelWindow(resolved, model); err != nil {
		return llm.ClientConfig{}, providerEntry{}, err
	}
	resolved = resolveOutputAllocation(resolved)

	cc, terr := a.clientConfigForEntry(reg.effective(), resolved, cfg, model, apiKey)
	if terr != nil {
		return llm.ClientConfig{}, providerEntry{}, terr
	}
	return cc, resolved, nil
}

const (
	defaultOutputReserve = llm.DefaultMaxOutputTokens
	promptSafetyTokens   = 2048
)

func discoveredWindow(m modelMeta, entry providerEntry, provider, model string) (window, reserve int) {
	window, reserve = m.Context, m.MaxOut
	known := entry.ContextLength
	if known == 0 {
		known = window
	}
	if reserve > 0 && known > 0 && reserve+promptSafetyTokens >= known {
		reserve = 0
	}
	return window, reserve
}

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
		return fmt.Errorf("provider %q model %q context_length %d must exceed output reserve %d plus safety margin %d — set a max_output_tokens on the entry small enough to leave the window room for a prompt",
			entry.Name, model, entry.ContextLength, reserve, promptSafetyTokens)
	}
	return nil
}

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
	logsink.Info("llm.budget", "provider %q thinking budget clamped %d -> %d — the entry left < %d visible output tokens of %d",
		entry.Name, requested, entry.ThinkingBudget, visibleFloor, output)
	return entry
}

func providerAPIKey(entry providerEntry, supplied, blockEnv string) string {
	if supplied != "" || entry.typedOnly {
		return supplied
	}
	if entry.APIKey != "" {
		return entry.APIKey
	}
	if entry.APIKeyEnv != "" {
		key := os.Getenv(entry.APIKeyEnv)
		if key == "" {

			logsink.Warn("llm.refusal", "provider %q names %s for its key and that variable is empty — "+
				"sending no credential rather than another provider's",
				entry.Name, entry.APIKeyEnv)
		}
		return key
	}
	if blockEnv != "" {
		if blockEnvIsFor(entry, blockEnv) {
			return os.Getenv(blockEnv)
		}
		if owner := vendorKeyEnvOwner(blockEnv); owner != entry.Name && os.Getenv(blockEnv) != "" {
			keyMismatchSaid(entry.Name, blockEnv, owner)
		}
	}
	env, withheld, home := lentKeyEnv(entry)
	if env != "" {
		return os.Getenv(env)
	}
	if withheld != "" && os.Getenv(withheld) != "" {
		keyElsewhereSaid(entry.Name, withheld, home, addressOrigin(entry.URL))
	}
	return ""
}

func lentKeyEnv(entry providerEntry) (env, withheld, home string) {
	vendors, home, atHome := vendorsKey(entry.Name, entry.URL)
	if own := entry.VendorKeyEnv; own != "" && (own != vendors || atHome) {
		return own, "", ""
	}
	if vendors != "" && !atHome {
		return "", vendors, home
	}
	return "", "", ""
}

func blockEnvIsFor(entry providerEntry, env string) bool {
	owner := vendorKeyEnvOwner(env)
	if owner == "" {
		return true
	}
	_, _, atHome := vendorsKey(entry.Name, entry.URL)
	return owner == entry.Name && atHome
}

func addressOrigin(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return ""
	}
	scheme, host, port := strings.ToLower(u.Scheme), strings.ToLower(u.Hostname()), u.Port()
	if scheme != "https" && scheme != "http" {
		return ""
	}
	if (scheme == "https" && port == "443") || (scheme == "http" && port == "80") {
		port = ""
	}
	switch {
	case port != "":
		host = net.JoinHostPort(host, port)
	case strings.Contains(host, ":"):
		host = "[" + host + "]"
	}
	return scheme + "://" + host
}

func sameOrigin(a, b string) bool {
	o := addressOrigin(a)
	return o != "" && o == addressOrigin(b)
}

func vendorsKey(name, address string) (env, home string, atHome bool) {
	shipped := entryNamed(embeddedRegistry(), name)
	if shipped == nil || shipped.VendorKeyEnv == "" {
		return "", "", false
	}
	return shipped.VendorKeyEnv, addressOrigin(shipped.URL), sameOrigin(shipped.URL, address)
}

func keyEnvFor(selected, blockEnv string, e providerEntry) string {
	if blockEnv == "" {
		return ""
	}
	if selected == e.Name || (selected == "" && e.Default) {
		return blockEnv
	}
	return ""
}

func llmKeyEnvForProvider(block LLMConfig, reg *providerRegistry, provider string) string {
	if block.APIKeyEnv == "" || reg == nil {
		return ""
	}
	if selected, err := selectProvider(block, reg); err == nil && selected.Name == provider {
		return block.APIKeyEnv
	}
	return ""
}

func (a *App) resolveRouteLLM(block LLMConfig, reg *providerRegistry, provider, model string) (llm.ClientConfig, providerEntry, error) {
	route := block
	route.Provider, route.Model, route.APIKeyEnv = provider, model, llmKeyEnvForProvider(block, reg, provider)
	return a.resolveLLMConfig(route, reg)
}

func keyEnvToExport(entry providerEntry, block LLMConfig) string {
	if entry.APIKeyEnv != "" {
		return entry.APIKeyEnv
	}
	if env := keyEnvFor(block.Provider, block.APIKeyEnv, entry); env != "" && blockEnvIsFor(entry, env) {
		return env
	}
	env, _, _ := lentKeyEnv(entry)
	return env
}

func keyRemedy(entry providerEntry, block LLMConfig) string {
	if env := keyEnvToExport(entry, block); env != "" {
		return "export " + env
	}
	return "name a variable for this entry (api_key_env) in providers.json"
}

func keyMismatchSaid(provider, env, owner string) {
	if _, said := keyMismatches.LoadOrStore(provider+"\x00"+env, true); said {
		return
	}
	logsink.Warn("llm.refusal", "provider %q is selected with the key variable %s, which is %s's own, and that variable is set — "+
		"it is not sent to %q. To give this endpoint that key, name the variable on its entry (api_key_env) in providers.json",
		provider, env, owner, provider)
}

func keyElsewhereSaid(provider, env, home, at string) {
	if _, said := keyMismatches.LoadOrStore(provider+"\x00"+env+"\x00"+at, true); said {
		return
	}
	logsink.Warn("llm.refusal", "provider %q is at %s, and the key variable %s is %s's own: it goes only to %s and was not sent — "+
		"to send a key to %s, store one on the entry or name a variable on it (api_key_env) in providers.json",
		provider, at, env, provider, home, at)
}

var keyMismatches sync.Map

func selectProvider(cfg LLMConfig, reg *providerRegistry) (*providerEntry, error) {
	var entry *providerEntry
	if name := cfg.Provider; name != "" {
		for i := range reg.Providers {
			if reg.Providers[i].Name == name {
				entry = &reg.Providers[i]
				break
			}
		}
		if entry == nil {
			if b := brokenNamed(reg, name); b != nil {
				return nil, fmt.Errorf("llm.provider %q is a broken entry in providers.json (%s); repair or remove it in Settings → Providers", name, b.reason)
			}
			return nil, fmt.Errorf("llm.provider %q is not in providers.json (%d providers)", name, len(reg.Providers))
		}
	} else {
		for i := range reg.Providers {
			if reg.Providers[i].Default {
				entry = &reg.Providers[i]
				break
			}
		}
		if entry == nil {
			for _, b := range reg.broken {
				if b.isDefault {
					return nil, fmt.Errorf("llm.provider is empty and the default-flagged entry %q in providers.json is broken (%s); repair or remove it in Settings → Providers", b.name, b.reason)
				}
			}
			return nil, fmt.Errorf("llm.provider is empty and providers.json flags no default provider")
		}
	}

	if !chatProvider(*entry) {
		if cfg.Provider != "" {
			return nil, fmt.Errorf("provider %q serves speech only — it names no chat model, or says chat: false; llm.provider must name a chat provider", entry.Name)
		}
		return nil, fmt.Errorf("the default provider %q serves speech only — it names no chat model, or says chat: false; flag a chat provider as default or set llm.provider", entry.Name)
	}
	return entry, nil
}

type budgetSource string

const (
	budgetDeclared budgetSource = "declared"
	budgetDerived  budgetSource = "derived"
	budgetFallback budgetSource = "fallback"
)

func promptBudgetFor(entry providerEntry, promptBudget int) (int, budgetSource) {
	source := budgetDeclared
	if promptBudget == 0 {
		source = budgetFallback
	}
	if cl := entry.ContextLength; cl > 0 {
		reserve := entry.MaxOutputTokens
		if reserve <= 0 {
			reserve = defaultOutputReserve
		}
		if derived := cl - reserve - promptSafetyTokens; derived > 0 && (promptBudget == 0 || derived < promptBudget) {
			logsink.Info("llm.budget", "Prompt budget derived from model window: %d (context %d - output %d - margin %d)", derived, cl, reserve, promptSafetyTokens)
			promptBudget, source = derived, budgetDerived
		}
	}
	if promptBudget == 0 {
		promptBudget = llm.DefaultContextBudgetTokens

		logsink.Warn("llm.budget", "Prompt budget: FALLBACK %d tokens — provider %q declares no context_length and none was discovered; the model's real window may be far larger. Set context_length on the provider entry (Settings → Providers).",
			promptBudget, entry.Name)
	}
	return promptBudget, source
}

func facilityBudgetFor(entry providerEntry, conversation int, source budgetSource) (int, budgetSource) {
	if cl := entry.ContextLength; cl > 0 {
		reserve := entry.MaxOutputTokens
		if reserve <= 0 {
			reserve = defaultOutputReserve
		}
		if derived := cl - reserve - promptSafetyTokens; derived > 0 {
			return derived, budgetDerived
		}
	}
	return conversation, source
}

func (s budgetSource) window() bool { return s == budgetDerived }

func (a *App) rememberPromptBudget(entry providerEntry, maxPromptTokens int) int {
	budget, source := promptBudgetFor(entry, maxPromptTokens)
	facility, facilitySource := facilityBudgetFor(entry, budget, source)
	a.providers.runtime.mu.Lock()
	a.providers.runtime.activeBudget, a.providers.runtime.activeBudgetSource = budget, source
	a.providers.runtime.activeFacilityBudget, a.providers.runtime.activeFacilityBudgetSource = facility, facilitySource
	a.providers.runtime.mu.Unlock()
	return budget
}

func (a *App) currentFacilityBudget() (int, budgetSource) {
	a.providers.runtime.mu.RLock()
	defer a.providers.runtime.mu.RUnlock()
	return a.providers.runtime.activeFacilityBudget, a.providers.runtime.activeFacilityBudgetSource
}

func (a *App) currentPromptBudget() (int, budgetSource) {
	a.providers.runtime.mu.RLock()
	defer a.providers.runtime.mu.RUnlock()
	return a.providers.runtime.activeBudget, a.providers.runtime.activeBudgetSource
}

func (a *App) activateLLMRuntime(client *llm.Client, entry providerEntry, maxPromptTokens int) {
	a.adoptRuntime(entry, client)
	promptBudget := a.rememberPromptBudget(entry, maxPromptTokens)
	if a.composer != nil {
		a.composer.SetMaxTokens(promptBudget)
	}
	if a.promptGate != nil {

		facilityBudget, source := a.currentFacilityBudget()
		a.promptGate.SetMaxTokens(facilityBudget)
		a.promptGate.SetWindowKnown(source.window())
	}

	a.cognitionReconsider()
	if a.conv != nil {
		a.conv.SetModelLimits(promptBudget, entry.ThinkingBudget)

		_, src := a.currentPromptBudget()
		a.conv.SetContextBudgetFallback(src == budgetFallback)
	}
	if a.ledger != nil {
		a.ledger.SetModelID(client.ModelName())
	}
	if a.llmSwap != nil {
		a.llmSwap.Swap(client)
	}
}

func (a *App) adoptRuntime(entry providerEntry, client *llm.Client) {
	a.providers.runtime.mu.Lock()
	a.providers.runtime.activeProvider, a.providers.runtime.activeClient = entry, client

	a.providers.runtime.credentialReplaced = entry.Credential != "" && entry.signInsSeen != a.providers.runtime.signIns
	a.providers.runtime.mu.Unlock()
}

func (a *App) signedInCredential(dest string) bool {

	a.providers.runtime.mu.Lock()
	a.providers.runtime.signIns++
	active := a.providers.runtime.activeProvider
	a.providers.runtime.mu.Unlock()
	if _, pinned := active.CredentialOptions["file"]; pinned || active.Credential == "" {
		return false
	}
	owned, err := a.ownedCredentialPath(active.Credential, active.signIn.CredentialFile)
	if err != nil {
		return false
	}
	if owned, err = filepath.Abs(owned); err != nil || owned != dest {
		return false
	}
	a.providers.runtime.mu.Lock()
	a.providers.runtime.credentialReplaced = true
	a.providers.runtime.mu.Unlock()
	return true
}

func (a *App) activeRuntime() (providerEntry, *llm.Client) {
	a.providers.runtime.mu.RLock()
	defer a.providers.runtime.mu.RUnlock()
	return a.providers.runtime.activeProvider, a.providers.runtime.activeClient
}

func (a *App) currentProvider() providerEntry {
	a.providers.runtime.mu.RLock()
	defer a.providers.runtime.mu.RUnlock()
	return a.providers.runtime.activeProvider
}

func (a *App) providerRuntimeMatches(reg *providerRegistry) bool {
	if a.llmSwap == nil {
		return true
	}
	entry, err := selectProvider(a.cfg.LLM, reg)
	if err != nil {
		return false
	}
	a.providers.runtime.mu.RLock()
	active, replaced := a.providers.runtime.activeProvider, a.providers.runtime.credentialReplaced
	a.providers.runtime.mu.RUnlock()
	if active.Name == "" {
		return true
	}
	if replaced {
		return false
	}
	model := a.cfg.LLM.Model
	if model == "" {
		model = entry.DefaultModel
	}
	desired := resolveOutputAllocation(limitToModel(*entry, model, reg.effective()))

	if entry.ContextLength == 0 {
		desired.ContextLength = limitModelWindow(active, model, reg.effective()).ContextLength
	}
	if entry.MaxOutputTokens == 0 {
		desired.MaxOutputTokens = limitModelOutput(active, model, reg.effective()).MaxOutputTokens
	}
	cap, _ := reg.effective().capabilityFor(model)
	if cap.MaxOutputTokens < 0 || cap.ContextLength < 0 {
		return false
	}
	subscription := entryDialect(*entry) == llm.DialectResponses
	if !a.llmSwap.Current().ModelContractMatches(cap.cacheBreakpoints() && !subscription, cap.modernOutputLimit() && !subscription, cap.CacheRetentions) {
		return false
	}
	return sameProviderRuntime(active, desired, a.cfg.LLM.Model != "")
}

func limitToModel(entry providerEntry, model string, eff *effectiveCaps) providerEntry {
	return limitModelOutput(limitModelWindow(entry, model, eff), model, eff)
}

func limitModelWindow(entry providerEntry, model string, eff *effectiveCaps) providerEntry {
	cap, ok := eff.capabilityFor(model)
	if !ok || cap.ContextLength <= 0 || (entry.ContextLength > 0 && entry.ContextLength <= cap.ContextLength) {
		return entry
	}
	if entry.ContextLength > 0 {
		logsink.Info("llm.budget", "LLM context window: provider %q model %q limited from %d to its declared window %d", entry.Name, model, entry.ContextLength, cap.ContextLength)
	}
	entry.ContextLength = cap.ContextLength
	return entry
}

func limitModelOutput(entry providerEntry, model string, eff *effectiveCaps) providerEntry {
	cap, ok := eff.capabilityFor(model)
	if ok && cap.MaxOutputTokens > 0 && entry.MaxOutputTokens > cap.MaxOutputTokens {
		logsink.Info("llm.budget", "LLM output allocation: provider %q model %q limited from %d to its declared maximum %d", entry.Name, model, entry.MaxOutputTokens, cap.MaxOutputTokens)
		entry.MaxOutputTokens = cap.MaxOutputTokens
	}
	return entry
}
