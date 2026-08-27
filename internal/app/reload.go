package app

import (
	"log"
	"os"
	"path/filepath"
	"reflect"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/fsdir"
	"github.com/aiii-dot-id/aii-os/internal/llm"
)

// --- Live config re-read (2026-08-18, James): "This should be a live
// grant, not a forced restart." ---
//
// The portable core is a FILE WATCHER, not a signal: config.json's
// mtime is observable identically on linux, macOS, windows, android,
// and ios, so one mechanism serves all five platforms with zero
// platform code. SIGHUP is the unix nicety layered on top
// (signal_reload_unix.go) — it triggers the SAME reload path. The
// dashboard remains the third trigger; all three converge here.

// watchConfig reloads on config-file change. Event-driven since
// 2026-08-26 (operator ruling: polling eats batteries): fsdir watches
// the PARENT DIRECTORY narrowed to the file's name, so an editor's
// atomic-rename save arrives as an event instead of an mtime race,
// and the heartbeat insures the drift cases. The mtime compare stays
// — the snapshot is the truth, the event only says "look".
func (a *App) watchConfig(path string) {
	if path == "" {
		return // no file to watch (embedded/test configs)
	}
	var last time.Time
	if fi, err := os.Stat(path); err == nil {
		last = fi.ModTime()
	}
	w := fsdir.New(a.bgCtx, a.gate, filepath.Dir(path), fsdir.Options{
		Heartbeat: a.watcherInterval(),
		File:      filepath.Base(path),
	})
	for {
		select {
		case <-a.bgCtx.Done():
			return
		case <-w.C:
			fi, err := os.Stat(path)
			if err != nil {
				continue
			}
			if fi.ModTime() != last {
				last = fi.ModTime()
				a.reloadConfig()
			}
		}
	}
}

// watcherInterval is the mtime-poll cadence shared by the config and
// ui-layout watchers: 2s, test-settable via a.watchEvery (the
// sessionGrace pattern — a field, not a flag).
func (a *App) watcherInterval() time.Duration {
	// Since 2026-08-26 this is the fsdir HEARTBEAT (drift insurance),
	// not the signal path — events carry edits in ~150ms. 45s replaces
	// the old 2s poll: the four watchers drop from ~43k wakeups/day
	// each to ~1.9k, and the tests still drive time through watchEvery.
	if a.watchEvery > 0 {
		return a.watchEvery
	}
	return fsdir.DefaultHeartbeat
}

// reloadConfig re-reads the config file and applies the LIVE-appliable
// subset. Everything else stays saved-for-next-boot, stated in the log
// — never silently.
//
// Live today: llm.* (the swappable client), tools.extra_roots (registry
// + the identity's floor — enforcement AND knowledge move together),
// tools.disabled.
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
	if llmChanged && a.llmSwap != nil {
		providerPath = a.providersPath()
		providers, err = loadProvidersFile(providerPath)
		if err == nil {
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
	// The operator's answer reaches the broker HERE, on every reload,
	// because that is where the answer changes — not in plugin
	// convergence, which returns early whenever no package changed.
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
}
