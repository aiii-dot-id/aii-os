package app

import (
	"os"
	"path/filepath"
	"reflect"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/fsdir"
	"github.com/aiii-dot-id/aii-os/internal/identity"
	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
)

func (a *App) watchConfig(path string) {
	if path == "" {
		return
	}
	providerPath := providerFilePath(path)
	config := fsdir.New(a.bgCtx, a.gate, filepath.Dir(path), fsdir.Options{Heartbeat: a.watcherInterval(), File: filepath.Base(path)})
	providers := fsdir.New(a.bgCtx, a.gate, filepath.Dir(providerPath), fsdir.Options{Heartbeat: a.watcherInterval(), File: filepath.Base(providerPath)})

	controlPath := LogControlPathIn(identityHomeFromConfig(path))
	if err := os.MkdirAll(filepath.Dir(controlPath), 0o700); err != nil {
		logsink.Warn("logs.error", "cannot watch %s (%v) — a level set there will not apply until the next boot", controlPath, err)
	}
	control := fsdir.New(a.bgCtx, a.gate, filepath.Dir(controlPath), fsdir.Options{Heartbeat: a.watcherInterval(), File: filepath.Base(controlPath)})
	for {
		select {
		case <-a.bgCtx.Done():
			return
		case <-config.C:
		case <-providers.C:
		case <-control.C:

			applyLogLevels(a.configSnapshot(), controlPath)
			applyLogTaps(a.configSnapshot(), controlPath)
			continue
		}

		a.reloadConfig()
	}
}

func (a *App) watcherInterval() time.Duration {

	if a.watchEvery > 0 {
		return a.watchEvery
	}
	return fsdir.DefaultHeartbeat
}

func (a *App) reloadConfig() {
	current := a.configSnapshot()
	if current.SourcePath == "" || !a.live.Load() {
		return
	}
	fresh, err := ReadConfig(current.SourcePath)
	if err != nil {
		logsink.Warn("config.error", "unreadable, keeping current: %v", err)
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

	var measured substrateCapability
	probed := false
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
				candidateBudget, _ := promptBudgetFor(entry, current.Prompt.MaxTokens)
				client = a.newLLMClient(cc, candidateBudget)
				if substrateChanged {
					measured, err = a.probeSubstrate(client, cc, entry, providers, fresh.LLM.ProbeTimeoutSeconds)
					probed = err == nil
				}
			}
		}
		if err != nil {
			llmChanged = false
			fresh.LLM = current.LLM
			logsink.Warn("config.refusal", "llm refused, current substrate kept: %v", err)
		}
	}

	holdTurn := llmChanged && a.llmSwap != nil
	if holdTurn {
		if a.bgCtx == nil {
			logsink.Warn("config.refusal", "application lifecycle is unavailable")
			return
		}
		if err := a.acquireTurn(a.bgCtx); err != nil {
			logsink.Warn("config.refusal", "cannot wait for current turn: %v", err)
			return
		}
	}
	a.cfgMu.Lock()
	latest, fileErr := ReadConfig(current.SourcePath)
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
			logsink.Warn("config.error", "recheck failed, keeping current: %v", fileErr)
		} else {
			logsink.Info("config.refusal", "superseded by a concurrent configuration change")
		}
		return
	}

	if llmChanged && holdTurn {
		a.activateLLMRuntime(client, entry, current.Prompt.MaxTokens)
		if probed {
			a.setSubstrateCapability(measured)
		}
	}
	rootsChanged := !reflect.DeepEqual(fresh.Tools.ExtraRoots, current.Tools.ExtraRoots)
	if rootsChanged {
		if a.toolReg != nil {
			a.toolReg.SetExtraRoots(fresh.Tools.ExtraRoots)
			a.loadRing5()
			_, fresh.Tools.ExtraRoots = a.toolReg.Roots()
		}
	}
	if fresh.Dashboard.AccessToken != current.Dashboard.AccessToken || fresh.Dashboard.RequireToken != current.Dashboard.RequireToken {
		if err := a.rearmDashboardToken(fresh.Dashboard); err != nil {
			logsink.Warn("dashboard.refusal", "%v", err)

			fresh.Dashboard.RequireToken = current.Dashboard.RequireToken
			fresh.Dashboard.AccessToken = current.Dashboard.AccessToken
		}
	}
	agencyChanged := !reflect.DeepEqual(fresh.Agency, current.Agency)
	policyChanged := !reflect.DeepEqual(fresh.Plugins.Grants, current.Plugins.Grants) ||
		!reflect.DeepEqual(fresh.Plugins.AuthProfiles, current.Plugins.AuthProfiles)

	savedForNextBoot := func() bool {
		a, b := current, *fresh
		blankLiveAppliable(&a)
		blankLiveAppliable(&b)
		return !reflect.DeepEqual(a, b)
	}()
	autoloadChanged := fresh.Plugins.Autoload != current.Plugins.Autoload
	catalogChanged := fresh.Plugins.CatalogURL != current.Plugins.CatalogURL
	maintenanceChanged := !reflect.DeepEqual(fresh.Maintenance, current.Maintenance) ||
		fresh.Identity.DBOptimizeLevels != current.Identity.DBOptimizeLevels ||
		fresh.Identity.DBLearnDictionary != current.Identity.DBLearnDictionary
	routeChanged := fresh.Certificate.RouteMode != current.Certificate.RouteMode ||
		fresh.Certificate.RelayEndpoint != current.Certificate.RelayEndpoint
	contactsChanged := !reflect.DeepEqual(fresh.Contacts, current.Contacts)
	readAtUseChanged := !reflect.DeepEqual(fresh.Plugins.Settings, current.Plugins.Settings) ||
		fresh.Updates.Automatic != current.Updates.Automatic ||
		fresh.Dashboard.Expert != current.Dashboard.Expert ||
		fresh.Dashboard.NoticesOffDashboard != current.Dashboard.NoticesOffDashboard
	togglesChanged := !reflect.DeepEqual(fresh.Tools.Disabled, current.Tools.Disabled)
	if togglesChanged {
		if a.toolReg != nil {

			if err := applyDisabledTools(a.toolReg, fresh.Tools.Disabled); err != nil {
				logsink.Warn("config.refusal", "tool toggles refused, current kept: %v", err)
				fresh.Tools.Disabled = current.Tools.Disabled
				togglesChanged = false
			}
		}
	}

	if fresh.Speech.Mode.Listen != current.Speech.Mode.Listen || fresh.Speech.Mode.Speak != current.Speech.Mode.Speak {
		if fresh.Speech.Mode.Revision <= current.Speech.Mode.Revision {
			fresh.Speech.Mode.Revision = current.Speech.Mode.Revision + 1
		}
	} else if fresh.Speech.Mode.Revision < current.Speech.Mode.Revision {
		fresh.Speech.Mode.Revision = current.Speech.Mode.Revision
	}

	applyLogLevels(*fresh, LogControlPathIn(identityHomeFromConfig(fresh.SourcePath)))
	applyLogTaps(*fresh, LogControlPathIn(identityHomeFromConfig(fresh.SourcePath)))
	*a.cfg = *fresh
	a.publishVoiceMode(fresh.Speech.Mode)

	a.applyAgency(fresh.Agency)
	rhythmChanged := fresh.Agency.RhythmSeconds != current.Agency.RhythmSeconds && a.timeFac != nil
	if rhythmChanged {
		if err := a.armRhythm(fresh.Agency.RhythmSeconds); err != nil {
			logsink.Warn("config.refusal", "metabolism rhythm not re-armed, it keeps its cadence until the next boot: %v", err)
			rhythmChanged = false
		}
	}
	a.cfgMu.Unlock()
	if !reflect.DeepEqual(current.Speech.Mode, fresh.Speech.Mode) {
		a.voiceModeCommitted(current.Speech.Mode, fresh.Speech.Mode)
	}

	a.replacePolicy(*fresh)
	if holdTurn {
		a.releaseTurn()
	}

	if llmChanged {
		logsink.Info("config.decision", "llm applied live (provider %q, model %q)", fresh.LLM.Provider, fresh.LLM.Model)
	}
	if rootsChanged {
		logsink.Info("config.decision", "sandbox roots applied live -> %v (floor updated)", fresh.Tools.ExtraRoots)
	}
	if autoloadChanged {
		logsink.Info("config.decision", "plugins.autoload -> %s", fresh.Plugins.Autoload)
		a.pokePluginSweep()
	}
	if catalogChanged {
		logsink.Info("config.decision", "plugins.catalog_url applied live -> %q (the catalog refreshes now)", fresh.Plugins.CatalogURL)
		a.catalog.Poke()
	}
	if maintenanceChanged {
		logsink.Info("config.decision", "maintenance and database housekeeping applied live (the daily pass reads them when it runs)")
	}
	if routeChanged {
		logsink.Info("config.decision", "public name route applied live -> %q (the route owner looks again now)", fresh.Certificate.RouteMode)
		a.signalRoute()
	}
	if contactsChanged {
		logsink.Info("config.decision", "contacts applied live -> %d line(s)", len(fresh.Contacts))
		a.pokeOutbox()
	}
	if readAtUseChanged {
		logsink.Info("config.decision", "plugin settings, automatic updates and the page's switches applied live (each is read where it is used)")
	}
	if togglesChanged {
		logsink.Info("config.decision", "tool toggles applied live -> disabled %v", fresh.Tools.Disabled)
	}
	if agencyChanged {
		ag, queue := fresh.Agency, "on"
		if !agencyOn(ag.SpawnQueue) {
			queue = "off"
		}
		logsink.Info("config.decision", "agency ceilings applied live -> depth %d, parallel %d (queue %s), mints %d, wall %ds, local wall %ds; a child %d rounds, %d calls, %d legs; a turn %d rounds, %d tokens",
			ag.MaxSubagentDepth, ag.MaxParallelSubagents, queue, ag.SubagentMaxMints, ag.SubagentWallSeconds,
			ag.SubagentWallSecondsLocal, ag.SubagentMaxToolRounds, ag.SubagentMaxToolCalls, ag.SubagentMaxLegs, ag.MaxToolRounds, ag.TurnTokenBudget)
	}
	if rhythmChanged {
		logsink.Info("config.decision", "metabolism rhythm re-armed live -> every %ds from now", fresh.Agency.RhythmSeconds)
	}
	if policyChanged {
		logsink.Info("config.decision", "plugin grants/auth profiles applied live -> %d grant(s), %d profile(s)",
			len(fresh.Plugins.Grants), len(fresh.Plugins.AuthProfiles))
	}
	if savedForNextBoot {
		logsink.Info("config.decision", "other settings changed and are SAVED FOR NEXT BOOT — this process keeps the values it started with")
	}
	if a.dashboard != nil && !reflect.DeepEqual(current, *fresh) {
		a.dashboard.BroadcastConfig()
	}
}

func (a *App) applyAgency(ag AgencyConfig) {
	queue := agencyOn(ag.SpawnQueue)
	if a.engine != nil {
		a.engine.SetAgencyLimits(ag.MaxSubagentDepth, ag.MaxParallelSubagents, ag.SubagentMaxMints, ag.SubagentWallSeconds)
		a.engine.SetLocalSpawnWall(ag.SubagentWallSecondsLocal)
		a.engine.SetSpawnQueue(queue)
		a.engine.SetSpawnBudget(ag.SubagentMaxToolRounds, ag.SubagentMaxToolCalls, ag.SubagentMaxLegs)
	}
	if a.store != nil {
		slots := 0
		if queue {
			slots = ag.MaxParallelSubagents
		}
		a.store.SetClaimLimit(identity.SubagentWorkKind, slots)
	}
	if a.conv != nil {
		a.conv.SetTurnBounds(ag.MaxToolRounds, ag.TurnTokenBudget)
	}
}

func blankLiveAppliable(c *Config) {
	c.contactsPresent = false
	c.Logs.Level = ""
	c.Logs.Detail = nil
	c.LLM = LLMConfig{}
	c.Speech = SpeechConfig{}
	c.Tools.ExtraRoots = nil
	c.Tools.Disabled = nil
	c.Plugins.Autoload = ""
	c.Plugins.CatalogURL = ""
	c.Plugins.Grants = nil
	c.Plugins.AuthProfiles = nil
	c.Maintenance = MaintenanceConfig{}
	c.Identity.DBOptimizeLevels = false
	c.Identity.DBLearnDictionary = false
	c.Plugins.Settings = nil
	c.Updates.Automatic = false
	c.Dashboard.Expert = false
	c.Dashboard.NoticesOffDashboard = false
	c.Contacts = nil
	c.Certificate.RouteMode = ""
	c.Certificate.RelayEndpoint = ""

	c.Agency = AgencyConfig{

		HeuristicNudges: c.Agency.HeuristicNudges,
		BreadthNudge:    c.Agency.BreadthNudge,
		PlanNudge:       c.Agency.PlanNudge,
		PlanningBrief:   c.Agency.PlanningBrief,

		QueueWorkers: c.Agency.QueueWorkers,

		OutcomeWindow: c.Agency.OutcomeWindow,
	}
	c.Dashboard.AccessToken = ""
	c.Dashboard.RequireToken = false
}
