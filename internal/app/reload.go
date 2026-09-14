package app

import (
	"log"
	"path/filepath"
	"reflect"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/fsdir"
	"github.com/aiii-dot-id/aii-os/internal/llm"
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
func (a *App) watchConfig(path string) {
	if path == "" {
		return
	}
	providerPath := providerFilePath(path)
	config := fsdir.New(a.bgCtx, a.gate, filepath.Dir(path), fsdir.Options{Heartbeat: a.watcherInterval(), File: filepath.Base(path)})
	providers := fsdir.New(a.bgCtx, a.gate, filepath.Dir(providerPath), fsdir.Options{Heartbeat: a.watcherInterval(), File: filepath.Base(providerPath)})
	for {
		select {
		case <-a.bgCtx.Done():
			return
		case <-config.C:
		case <-providers.C:
		}
		// .
		// .
		a.reloadConfig()
	}
}

// .
// .
// .
func (a *App) watcherInterval() time.Duration {
	// .
	// .
	// .
	// .
	if a.watchEvery > 0 {
		return a.watchEvery
	}
	return fsdir.DefaultHeartbeat
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
func (a *App) reloadConfig() {
	current := a.configSnapshot()
	if current.SourcePath == "" || !a.live {
		return
	}
	fresh, err := LoadConfig(current.SourcePath)
	if err != nil {
		log.Printf("Config reload: unreadable, keeping current: %v", err)
		return
	}
	loaded := *fresh

	llmChanged := fresh.LLM != current.LLM
	substrateChanged := fresh.LLM.Provider != current.LLM.Provider ||
		fresh.LLM.Model != current.LLM.Model ||
		fresh.LLM.APIKeyEnv != current.LLM.APIKeyEnv
	var client *llm.Client
	var entry providerEntry
	var providers *providerRegistry
	var providerPath string
	if a.llmSwap != nil {
		providerPath = a.providersPath()
		providers, err = loadProvidersFile(providerPath)
		if err == nil {
			a.cfgMu.RLock()
			providerChanged := !a.providerRuntimeMatches(providers)
			a.cfgMu.RUnlock()
			llmChanged = llmChanged || providerChanged
		}
		if err == nil && llmChanged {
			var cc llm.ClientConfig
			cc, entry, err = a.resolveLLMConfig(fresh.LLM, providers)
			if err == nil {
				client = a.newLLMClient(cc, promptBudgetFor(entry, current.Prompt.MaxTokens))
				if substrateChanged {
					err = a.probeSubstrate(client, cc, entry)
				}
			}
		}
		if err != nil {
			llmChanged = false
			fresh.LLM = current.LLM
			log.Printf("Config reload: llm refused, current substrate kept: %v", err)
		}
	}

	holdTurn := llmChanged && a.llmSwap != nil
	if holdTurn {
		if a.bgCtx == nil {
			log.Printf("Config reload: application lifecycle is unavailable")
			return
		}
		if err := a.acquireTurn(a.bgCtx); err != nil {
			log.Printf("Config reload: cannot wait for current turn: %v", err)
			return
		}
	}
	a.cfgMu.Lock()
	latest, fileErr := LoadConfig(current.SourcePath)
	providersUnchanged := true
	if providers != nil {
		reg, err := loadProvidersFile(providerPath)
		providersUnchanged = err == nil && reflect.DeepEqual(reg, providers)
	}
	if fileErr != nil || !reflect.DeepEqual(latest, &loaded) || !reflect.DeepEqual(*a.cfg, current) || !providersUnchanged {
		a.cfgMu.Unlock()
		if holdTurn {
			a.releaseTurn()
		}
		if fileErr != nil {
			log.Printf("Config reload: recheck failed, keeping current: %v", fileErr)
		} else {
			log.Printf("Config reload: superseded by a concurrent configuration change")
		}
		return
	}

	if llmChanged && holdTurn {
		a.activateLLMRuntime(client, entry, current.Prompt.MaxTokens)
	}
	rootsChanged := !reflect.DeepEqual(fresh.Tools.ExtraRoots, current.Tools.ExtraRoots)
	if rootsChanged {
		if a.toolReg != nil {
			a.toolReg.SetExtraRoots(fresh.Tools.ExtraRoots)
			a.loadRing5()
			_, fresh.Tools.ExtraRoots = a.toolReg.Roots()
		}
	}
	agencyChanged := !reflect.DeepEqual(fresh.Agency, current.Agency)
	policyChanged := !reflect.DeepEqual(fresh.Plugins.Grants, current.Plugins.Grants) ||
		!reflect.DeepEqual(fresh.Plugins.AuthProfiles, current.Plugins.AuthProfiles)
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	savedForNextBoot := func() bool {
		a, b := current, *fresh
		blankLiveAppliable(&a)
		blankLiveAppliable(&b)
		return !reflect.DeepEqual(a, b)
	}()
	autoloadChanged := fresh.Plugins.Autoload != current.Plugins.Autoload
	togglesChanged := !reflect.DeepEqual(fresh.Tools.Disabled, current.Tools.Disabled)
	if togglesChanged {
		if a.toolReg != nil {
			disabled := make(map[string]bool, len(fresh.Tools.Disabled))
			for _, name := range fresh.Tools.Disabled {
				disabled[name] = true
			}
			for _, state := range a.toolReg.ToolStates() {
				a.toolReg.SetToolEnabled(state.Name, !disabled[state.Name])
			}
		}
	}
	*a.cfg = *fresh
	a.cfgMu.Unlock()
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
	if a.engine != nil {
		a.engine.SetAgencyLimits(fresh.Agency.MaxSubagentDepth, fresh.Agency.MaxParallelSubagents,
			fresh.Agency.SubagentMaxMints, fresh.Agency.SubagentWallSeconds)
		a.engine.SetLocalSpawnWall(fresh.Agency.SubagentWallSecondsLocal)
	}
	// .
	// .
	// .
	a.replacePolicy(*fresh)
	if holdTurn {
		a.releaseTurn()
	}

	if llmChanged {
		log.Printf("Config reload: llm applied live (provider %q, model %q)", fresh.LLM.Provider, fresh.LLM.Model)
	}
	if rootsChanged {
		log.Printf("Config reload: sandbox roots applied live -> %v (floor updated)", fresh.Tools.ExtraRoots)
	}
	if autoloadChanged {
		log.Printf("Config reload: plugins.autoload -> %s", fresh.Plugins.Autoload)
		a.pokePluginSweep()
	}
	if togglesChanged {
		log.Printf("Config reload: tool toggles applied live -> disabled %v", fresh.Tools.Disabled)
	}
	if agencyChanged {
		log.Printf("Config reload: agency ceilings applied live -> depth %d, parallel %d, mints %d, wall %ds, local wall %ds",
			fresh.Agency.MaxSubagentDepth, fresh.Agency.MaxParallelSubagents,
			fresh.Agency.SubagentMaxMints, fresh.Agency.SubagentWallSeconds, fresh.Agency.SubagentWallSecondsLocal)
	}
	if policyChanged {
		log.Printf("Config reload: plugin grants/auth profiles applied live -> %d grant(s), %d profile(s)",
			len(fresh.Plugins.Grants), len(fresh.Plugins.AuthProfiles))
	}
	if savedForNextBoot {
		log.Printf("Config reload: other settings changed and are SAVED FOR NEXT BOOT — this process keeps the values it started with")
	}
}

// .
// .
// .
// .
// .
func blankLiveAppliable(c *Config) {
	c.LLM = LLMConfig{}
	c.Tools.ExtraRoots = nil
	c.Tools.Disabled = nil
	c.Plugins.Autoload = ""
	c.Plugins.Grants = nil
	c.Plugins.AuthProfiles = nil
	c.Agency = AgencyConfig{}
}
