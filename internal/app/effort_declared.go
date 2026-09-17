package app

import (
	"fmt"
	"slices"
	"strings"
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
func declaredEffortLevels(eff *effectiveCaps, model string) []string {
	row, ok := eff.capabilityFor(model)
	if !ok || len(row.Effort) == 0 {
		return nil
	}
	return row.Effort
}

// .
// .
func vendorsOwnEntry(e providerEntry) bool {
	shipped := entryNamed(embeddedRegistry(), e.Name)
	return shipped != nil && strings.TrimRight(shipped.URL, "/") == strings.TrimRight(e.URL, "/")
}

// .
// .
// .
// .
func effortOnlyDeclaredChange(prev, next providerEntry, modelPinned bool, eff *effectiveCaps, model string) bool {
	if prev.ReasoningEffort == next.ReasoningEffort {
		return false
	}
	levels := declaredEffortLevels(eff, model)
	if levels == nil {
		return false
	}
	if next.ReasoningEffort != "" && !slices.Contains(levels, next.ReasoningEffort) {
		return false
	}
	prev.ReasoningEffort, next.ReasoningEffort = "", ""
	return sameProviderRuntime(prev, next, modelPinned)
}

// .
// .
// .
// .
// .
// .
// .
func (a *App) providerRuntimeMatchesBarEffort(reg *providerRegistry) bool {
	entry, err := selectProvider(a.cfg.LLM, reg)
	if err != nil {
		return false
	}
	running := *reg
	running.Providers = append([]providerEntry(nil), reg.Providers...)
	for i := range running.Providers {
		if running.Providers[i].Name == entry.Name {
			running.Providers[i].ReasoningEffort = a.currentProvider().ReasoningEffort
		}
	}
	return a.providerRuntimeMatches(&running)
}

// .
// .
// .
// .
// .
// .
func (a *App) setActiveEffort(level string) error {
	reg, err := a.loadProviders()
	if err != nil {
		return err
	}
	cc, entry, err := a.resolveLLMConfig(a.configSnapshot().LLM, reg)
	if err != nil {
		return err
	}
	levels := effortLevelsFor(reg.effective(), entry, cc.Model)
	if len(levels) == 0 {
		return fmt.Errorf("%s takes no effort level", cc.Model)
	}
	if level != "" && !slices.Contains(levels, level) {
		return fmt.Errorf("%q is not an effort level %s takes (%s)", level, cc.Model, strings.Join(levels, ", "))
	}
	return a.changeProviders(entry.Name, func(r *providerRegistry) error {
		for i := range r.Providers {
			if r.Providers[i].Name == entry.Name {
				r.Providers[i].ReasoningEffort = level
				return nil
			}
		}
		return fmt.Errorf("no provider named %q", entry.Name)
	})
}
