// .
// .
// .
// .
package app

import (
	"fmt"
	"log"
	"os"
	"reflect"

	"github.com/aiii-dot-id/aii-os/internal/llm"
)

func sameProviderRuntime(a, b providerEntry, modelPinned bool) bool {
	a.SubscribeURL, b.SubscribeURL = "", ""
	a.Default, b.Default = false, false
	a.Models, b.Models = nil, nil
	a.Chat, b.Chat = nil, nil
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
	// .
	// .
	// .
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
	resolved = limitModelOutput(resolved, model, reg.effective())
	if err := validateModelWindow(resolved, model); err != nil {
		return llm.ClientConfig{}, providerEntry{}, err
	}
	resolved = resolveOutputAllocation(resolved)

	// .
	// .
	// .
	// .
	cc, terr := a.clientConfigForEntry(reg.effective(), resolved, cfg, model, apiKey)
	if terr != nil {
		return llm.ClientConfig{}, providerEntry{}, terr
	}
	return cc, resolved, nil
}

const (
	// .
	// .
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
			// .
			// .
			log.Printf("LLM: provider %q names %s for its key and that variable is empty — "+
				"sending no credential rather than another provider's",
				entry.Name, entry.APIKeyEnv)
		}
		return key
	}
	return os.Getenv(fallbackEnv)
}

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
			return nil, fmt.Errorf("llm.provider is empty and providers.json flags no default provider")
		}
	}
	// .
	// .
	// .
	// .
	// .
	if !chatProvider(*entry) {
		if cfg.Provider != "" {
			return nil, fmt.Errorf("provider %q serves speech only — it names no chat model, or says chat: false; llm.provider must name a chat provider", entry.Name)
		}
		return nil, fmt.Errorf("the default provider %q serves speech only — it names no chat model, or says chat: false; flag a chat provider as default or set llm.provider", entry.Name)
	}
	return entry, nil
}

// .
// .
// .
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
		// .
		// .
		// .
		// .
		log.Printf("Prompt budget: FALLBACK %d tokens — provider %q declares no context_length and none was discovered; the model's real window may be far larger. Set context_length on the provider entry (Settings → Providers).",
			promptBudget, entry.Name)
	}
	return promptBudget
}

// .
// .
func (a *App) activateLLMRuntime(client *llm.Client, entry providerEntry, maxPromptTokens int) {
	a.activeProviderMu.Lock()
	a.activeProvider = entry
	a.activeProviderMu.Unlock()
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

// .
func (a *App) currentProvider() providerEntry {
	a.activeProviderMu.RLock()
	defer a.activeProviderMu.RUnlock()
	return a.activeProvider
}

// .
// .
// .
func (a *App) providerRuntimeMatches(reg *providerRegistry) bool {
	if a.llmSwap == nil {
		return true
	}
	entry, err := selectProvider(a.cfg.LLM, reg)
	if err != nil {
		return false
	}
	active := a.currentProvider()
	if active.Name == "" {
		return true
	}
	model := a.cfg.LLM.Model
	if model == "" {
		model = entry.DefaultModel
	}
	desired := resolveOutputAllocation(limitModelOutput(*entry, model, reg.effective()))
	// .
	if desired.ContextLength == 0 {
		desired.ContextLength = active.ContextLength
	}
	if entry.MaxOutputTokens == 0 {
		desired.MaxOutputTokens = limitModelOutput(active, model, reg.effective()).MaxOutputTokens
	}
	cap, _ := reg.effective().capabilityFor(model)
	if cap.MaxOutputTokens < 0 {
		return false
	}
	subscription := entryDialect(*entry) == llm.DialectResponses
	if !a.llmSwap.Current().ModelContractMatches(cap.cacheBreakpoints() && !subscription, cap.modernOutputLimit() && !subscription, cap.CacheRetentions) {
		return false
	}
	return sameProviderRuntime(active, desired, a.cfg.LLM.Model != "")
}

// .
// .
// .
// .
func limitModelOutput(entry providerEntry, model string, eff *effectiveCaps) providerEntry {
	cap, ok := eff.capabilityFor(model)
	if ok && cap.MaxOutputTokens > 0 && entry.MaxOutputTokens > cap.MaxOutputTokens {
		log.Printf("LLM output allocation: provider %q model %q limited from %d to its declared maximum %d", entry.Name, model, entry.MaxOutputTokens, cap.MaxOutputTokens)
		entry.MaxOutputTokens = cap.MaxOutputTokens
	}
	return entry
}
