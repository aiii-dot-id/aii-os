package app

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sync"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/continuity"
	"github.com/aiii-dot-id/aii-os/internal/fsdir"
	"github.com/aiii-dot-id/aii-os/internal/identity"
	"github.com/aiii-dot-id/aii-os/internal/limits"
	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
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

			a.applyLogOverlay(controlPath)
			continue
		}

		a.reloadIfChanged()
	}
}

func (a *App) applyLogOverlay(controlPath string) {
	a.cfgMu.RLock()
	defer a.cfgMu.RUnlock()
	cfg := *a.cfg
	overlayStep("read")
	applyLogLevels(cfg, controlPath)
	applyLogTaps(cfg, controlPath)
}

type reloadSum struct {
	config, providers [sha256.Size]byte
}

type reloadMemory struct {
	mu sync.Mutex

	seen    reloadSum
	applied bool

	refusals  int
	named     LLMConfig
	refusedAt time.Time
	retryAt   time.Time

	saved bool

	roots []string

	now func() time.Time
}

func (m *reloadMemory) clock() time.Time {
	if m.now != nil {
		return m.now()
	}
	return time.Now()
}

func (m *reloadMemory) due(sum reloadSum, asked bool, began time.Time) (run, changed bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.seen != sum {
		m.seen, m.applied = sum, false
		return true, true
	}
	if asked || !m.applied {
		return true, false
	}
	return m.refusals > 0 && !began.Before(m.retryAt), false
}

func (m *reloadMemory) standing() (named LLMConfig, refusals int, since time.Time, saved bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.named, m.refusals, m.refusedAt, m.saved
}

func (m *reloadMemory) waiting(began time.Time) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.refusals > 0 && began.Before(m.retryAt)
}

func (m *reloadMemory) refused(named LLMConfig, began time.Time, beat, ceiling time.Duration) (first bool, next time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.clock()
	if m.refusals == 0 {
		m.named, m.refusedAt, m.saved = named, now, false
	}
	m.refusals++
	end := began.Add(refusedRetryWait(m.refusals, beat, ceiling))
	m.retryAt = end.Add(-beat / 2)
	return m.refusals == 1, max(end.Sub(now), 0).Round(time.Second)
}

func (m *reloadMemory) ownSave() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.saved = m.refusals > 0
}

func (m *reloadMemory) soon() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.retryAt = time.Time{}
}

func (m *reloadMemory) end() (refusals int, since time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	refusals, since = m.refusals, m.refusedAt
	m.refusals, m.saved = 0, false
	return refusals, since
}

func (m *reloadMemory) settle(sum reloadSum) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.seen == sum {
		m.applied = true
	}
}

func (m *reloadMemory) spelled(named, held []string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.roots = nil
	if !slices.Equal(named, held) {
		m.roots = slices.Clone(named)
	}
}

func (m *reloadMemory) rootsSpelled() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.roots
}

const (
	refusedRetryMaxDefaultSeconds = 3600
	refusedRetryMaxMinSeconds     = int(fsdir.DefaultHeartbeat / time.Second)
	refusedRetryMaxMaxSeconds     = 86400
)

func validRefusedRetryMax(seconds int) error {
	if seconds != 0 && (seconds < refusedRetryMaxMinSeconds || seconds > refusedRetryMaxMaxSeconds) {
		return fmt.Errorf("llm.refused_retry_max_seconds must be zero (take the default, %d) or between %d and %d", refusedRetryMaxDefaultSeconds, refusedRetryMaxMinSeconds, refusedRetryMaxMaxSeconds)
	}
	return nil
}

func (l LLMConfig) refusedRetryMax() time.Duration {
	if l.RefusedRetryMaxSeconds <= 0 {
		return refusedRetryMaxDefaultSeconds * time.Second
	}
	return time.Duration(l.RefusedRetryMaxSeconds) * time.Second
}

func llmRuntimeChanged(fresh, current LLMConfig) bool {
	fresh.RefusedRetryMaxSeconds, current.RefusedRetryMaxSeconds = 0, 0
	return fresh != current
}

func refusedRetryWait(refusals int, beat, ceiling time.Duration) time.Duration {
	wait := beat
	for i := 1; i < refusals && wait < ceiling; i++ {
		wait *= 2
	}
	return min(wait, ceiling)
}

func (a *App) watcherInterval() time.Duration {

	if a.watchEvery > 0 {
		return a.watchEvery
	}
	return fsdir.DefaultHeartbeat
}

func (a *App) reloadConfig() { a.reload(true) }

func (a *App) reloadIfChanged() { a.reload(false) }

func (a *App) followFilesystem(cfg Config) {
	if a.toolReg == nil {
		return
	}
	placesMoved, rootsMoved := a.applySubstrate(cfg), false
	if named := a.reloads.rootsSpelled(); named != nil {

		a.cfgMu.Lock()
		if slices.Equal(a.cfg.Tools.ExtraRoots, cfg.Tools.ExtraRoots) && a.toolReg.ExtraRootsMoved(named) {
			a.toolReg.SetExtraRoots(named)
			_, a.cfg.Tools.ExtraRoots = a.toolReg.Roots()
			logsink.Info("config.decision", "sandbox roots applied live -> %v (floor updated)", a.cfg.Tools.ExtraRoots)
			rootsMoved = true
		}
		a.cfgMu.Unlock()
	}
	if placesMoved || rootsMoved {
		a.loadRing5()
	}
	if rootsMoved && a.dashboard != nil {
		a.dashboard.BroadcastConfig()
	}
}

func (a *App) reload(asked bool) {
	current := a.configSnapshot()
	if current.SourcePath == "" || !a.live.Load() {
		return
	}
	began := a.reloads.clock()

	data, err := readConfigBytes(current.SourcePath)
	if err != nil {
		logsink.Warn("config.error", "unreadable, keeping current: %v", err)
		return
	}
	sum := reloadSum{config: sha256.Sum256(data)}
	var providerPath string
	var providerData []byte
	var providerErr error
	if a.llmSwap != nil {
		providerPath = a.providersPath()
		if providerData, providerErr = readProvidersBytes(providerPath); providerErr == nil {
			sum.providers = sha256.Sum256(providerData)
		}
	}
	run, changed := a.reloads.due(sum, asked, began)
	if !run {
		a.followFilesystem(current)
		return
	}
	fresh, err := decodeConfig(current.SourcePath, data)
	if err != nil {
		logsink.Warn("config.error", "unreadable, keeping current: %v", err)

		a.reloads.end()
		a.reloads.settle(sum)
		return
	}
	loaded := *fresh

	if sameWritten(current, *fresh) && maps.Equal(current.cognitionHeld, fresh.cognitionHeld) {
		*fresh = current
	}

	if named, refusals, since, saved := a.reloads.standing(); refusals > 0 && !sameLLM(fresh.LLM, named) {
		a.reloads.end()
		if sameLLM(fresh.LLM, current.LLM) {
			by := "config.json states the running one again"
			if saved {
				by = "a save of this host's own configuration wrote the running one over it, as every such save writes the whole running configuration"
			}
			logsink.Warn("config.refusal", "the llm section this host refused (provider %q, model %q; refused %d time(s) since %s) is not tried any more: %s. State it in config.json again to have it tried",
				named.Provider, named.Model, refusals, since.Format("2006/01/02 15:04:05"), by)
		}
	}

	waiting := !changed && !asked && a.reloads.waiting(began)

	llmChanged := llmRuntimeChanged(fresh.LLM, current.LLM) || !reflect.DeepEqual(fresh.Overrides, current.Overrides)
	substrateChanged := fresh.LLM.Provider != current.LLM.Provider ||
		fresh.LLM.Model != current.LLM.Model ||
		fresh.LLM.APIKeyEnv != current.LLM.APIKeyEnv
	var client *llm.Client
	var entry providerEntry
	var providers *providerRegistry

	var measured substrateCapability
	probed := false

	retryCeiling := fresh.LLM.refusedRetryMax()
	llmRefused := false
	if a.llmSwap != nil && waiting {
		llmChanged, llmRefused = false, true
		fresh.LLM = current.LLM
	} else if a.llmSwap != nil {
		err = providerErr
		if err == nil {
			providers, err = decodeProviders(providerPath, providerData)
		}
		if err == nil {
			sayUnusedOverrides(fresh.Overrides, providers)
			a.cfgMu.RLock()
			providerChanged := !a.providerRuntimeMatches(providers)
			a.cfgMu.RUnlock()
			llmChanged = llmChanged || providerChanged
		}
		if err == nil && llmChanged {
			var cc llm.ClientConfig
			cc, entry, err = a.resolveLLMConfig(*fresh, providers)
			if err == nil {
				candidateBudget, _ := promptBudgetFor(entry, promptCeilingFor(*fresh, entry, cc.Model))
				client = a.newLLMClient(cc, candidateBudget)
				if substrateChanged {
					measured, err = a.probeSubstrate(client, cc, entry, providers, fresh.LLM.ProbeTimeoutSeconds)
					probed = err == nil
				}
			}
		}
		if err != nil {
			named := fresh.LLM
			llmChanged = false
			fresh.LLM = current.LLM
			llmRefused = true

			first, next := a.reloads.refused(named, began, a.watcherInterval(), retryCeiling)
			if first || asked {
				over := ""
				if !sameLLM(named, current.LLM) {
					over = " A save of this host's own configuration (a setting changed on the page, by voice or by a plugin) writes the running llm section over it in config.json; that ends the tries, and is said here when it happens."
				}
				logsink.Warn("config.refusal", "llm refused, current substrate kept: %v. It is tried again with no edit for as long as the files state it: next in about %s, each wait twice the last, up to one try every %s (llm.refused_retry_max_seconds).%s", err, next, retryCeiling, over)
			}
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
		a.activateLLMRuntime(client, entry, current)
		if probed {
			a.setSubstrateCapability(measured)
		}
	}
	tokenRefused := false
	rootsNamed := fresh.Tools.ExtraRoots
	rootsChanged := !reflect.DeepEqual(fresh.Tools.ExtraRoots, current.Tools.ExtraRoots)
	sandboxSwitched := fresh.Tools.SandboxOff != current.Tools.SandboxOff
	if (rootsChanged || sandboxSwitched) && a.toolReg != nil {
		if rootsChanged {
			a.toolReg.SetExtraRoots(fresh.Tools.ExtraRoots)
			_, fresh.Tools.ExtraRoots = a.toolReg.Roots()
		}
		if sandboxSwitched {

			if err := a.moveSandbox(*fresh, fresh.Tools.SandboxOff); err != nil {
				logsink.Warn("config.refusal", "tools.sandbox_off not applied: %v", err)
				sandboxSwitched = false
			}
		}
		a.loadRing5()
	}
	if fresh.Dashboard.AccessToken != current.Dashboard.AccessToken || fresh.Dashboard.RequireToken != current.Dashboard.RequireToken {
		if err := a.rearmDashboardToken(fresh.Dashboard); err != nil {
			logsink.Warn("dashboard.refusal", "%v", err)

			fresh.Dashboard.RequireToken = current.Dashboard.RequireToken
			fresh.Dashboard.AccessToken = current.Dashboard.AccessToken
			tokenRefused = true
		}

		if a.toolReg != nil && a.toolReg.SandboxOff() && !a.accessTokenInForce() {
			a.holdSandboxOn(*fresh, errSandboxHeldNoToken)
			a.loadRing5()
		}
	}

	if !reflect.DeepEqual(fresh.Dashboard.ApprovedScripts, current.Dashboard.ApprovedScripts) && a.dashboard != nil {
		a.dashboard.SetApprovedScripts(fresh.Dashboard.ApprovedScripts)
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
		fresh.Identity.DBLearnDictionary != current.Identity.DBLearnDictionary ||
		fresh.Plugins.Runtime.ReplacedMemoryDays != current.Plugins.Runtime.ReplacedMemoryDays

	routeChanged := fresh.Certificate.RouteMode != current.Certificate.RouteMode ||
		fresh.Certificate.RelayEndpoint != current.Certificate.RelayEndpoint ||
		!reflect.DeepEqual(fresh.Certificate.DirectAddresses, current.Certificate.DirectAddresses) ||
		!reflect.DeepEqual(fresh.Certificate.Prepare, current.Certificate.Prepare) ||
		fresh.Certificate.PrepareCertificate != current.Certificate.PrepareCertificate
	contactsChanged := !reflect.DeepEqual(fresh.Contacts, current.Contacts)
	readAtUseChanged := !reflect.DeepEqual(fresh.Plugins.Settings, current.Plugins.Settings) ||
		fresh.Updates.Automatic != current.Updates.Automatic ||
		fresh.Dashboard.Expert != current.Dashboard.Expert ||
		fresh.Dashboard.AccessCodeSeconds != current.Dashboard.AccessCodeSeconds ||
		fresh.Dashboard.RefusedPauses != current.Dashboard.RefusedPauses ||
		fresh.Dashboard.UnsignedRequestsPerSecond != current.Dashboard.UnsignedRequestsPerSecond ||
		fresh.Dashboard.UnsignedRequestBurst != current.Dashboard.UnsignedRequestBurst ||
		fresh.Dashboard.Record != current.Dashboard.Record ||
		!reflect.DeepEqual(fresh.Dashboard.SentRequests, current.Dashboard.SentRequests) ||
		!reflect.DeepEqual(fresh.Dashboard.channelSettings(), current.Dashboard.channelSettings()) ||
		fresh.Certificate.RelayPingSeconds != current.Certificate.RelayPingSeconds ||
		fresh.Certificate.RelayBackoffBaseMS != current.Certificate.RelayBackoffBaseMS ||
		fresh.Certificate.RelayBackoffMaxMS != current.Certificate.RelayBackoffMaxMS ||
		fresh.Certificate.RelayAnswerSeconds != current.Certificate.RelayAnswerSeconds ||
		fresh.Certificate.RelayCarriedMax != current.Certificate.RelayCarriedMax ||
		fresh.Certificate.RelayAttachesPerSecond != current.Certificate.RelayAttachesPerSecond ||
		fresh.Certificate.RelayAttachBurst != current.Certificate.RelayAttachBurst ||
		fresh.Dashboard.NoticesOffDashboard != current.Dashboard.NoticesOffDashboard
	limitsChanged := pluginLimitsMoved(&current, fresh)
	if limitsChanged {

		if err := pluginLimits(&pluginhost.Options{}, *fresh); err != nil {
			logsink.Warn("config.refusal", "plugin limits refused, current kept: %v", err)

			was := fresh.Plugins.Runtime
			fresh.Plugins.Runtime, fresh.Plugins.Resources = current.Plugins.Runtime, current.Plugins.Resources
			fresh.Plugins.Runtime.SlowStorageCallMs = was.SlowStorageCallMs
			fresh.Plugins.Runtime.MaxMemories, fresh.Plugins.Runtime.ReplacedMemoryDays = was.MaxMemories, was.ReplacedMemoryDays
			fresh.Plugins.Runtime.MaxInflightReceipts = was.MaxInflightReceipts
			limitsChanged = false
		}
	}
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

	if a.store != nil {
		a.store.SetOperatorVoices(fresh.Speech.OperatorSpeakers)
	}

	a.applyAgency(*fresh)
	rhythmChanged := fresh.Agency.RhythmSeconds != current.Agency.RhythmSeconds && a.timeFac != nil
	if rhythmChanged {
		if err := a.armRhythm(fresh.Agency.RhythmSeconds); err != nil {
			logsink.Warn("config.refusal", "metabolism rhythm not re-armed, it keeps its cadence until the next boot: %v", err)
			rhythmChanged = false
		}
	}

	cognitionChanged := cognitionMoved(current, *fresh)
	if cognitionChanged {
		a.applyCognition(*fresh)
	}
	a.cfgMu.Unlock()
	if !reflect.DeepEqual(current.Speech.Mode, fresh.Speech.Mode) {
		a.voiceModeCommitted(current.Speech.Mode, fresh.Speech.Mode)
	}

	reloadStep("published")
	a.replacePolicy()
	if holdTurn {
		a.releaseTurn()
	}

	refusals, since := 0, time.Time{}
	if !llmRefused {
		refusals, since = a.reloads.end()
	}
	if !tokenRefused {
		a.reloads.settle(sum)
		if a.toolReg != nil {
			_, held := a.toolReg.Roots()
			a.reloads.spelled(rootsNamed, held)
		}
	}

	if llmChanged {
		if refusals > 0 {
			logsink.Info("config.decision", "llm applied live (provider %q, model %q): it answers now, after %d refused tries since %s", fresh.LLM.Provider, fresh.LLM.Model, refusals, since.Format("2006/01/02 15:04:05"))
		} else {
			logsink.Info("config.decision", "llm applied live (provider %q, model %q)", fresh.LLM.Provider, fresh.LLM.Model)
		}
	} else if refusals > 0 {

		logsink.Info("config.decision", "llm no longer refused: the files resolve to the running substrate again (provider %q, model %q), after %d refused tries since %s", fresh.LLM.Provider, fresh.LLM.Model, refusals, since.Format("2006/01/02 15:04:05"))
	}
	if rootsChanged {
		logsink.Info("config.decision", "sandbox roots applied live -> %v (floor updated)", fresh.Tools.ExtraRoots)
	}
	if sandboxSwitched {
		logsink.Info("config.decision", "%s", sandboxSwitchLine(fresh.Tools.SandboxOff, "config file, applied live, floor updated"))
	}
	if fresh.Memory.MeaningSource != current.Memory.MeaningSource {
		logsink.Info("config.decision", "memory.meaning_source applied live -> %q (the source is chosen again at its next use)", fresh.Memory.MeaningSource)
	}
	if !reflect.DeepEqual(fresh.Logs.Group, current.Logs.Group) || !reflect.DeepEqual(fresh.Logs.Taps, current.Logs.Taps) {
		logsink.Info("config.decision", "logs.group and logs.taps applied live")
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
		a.pn.Signal()
	}
	if contactsChanged {
		logsink.Info("config.decision", "contacts applied live -> %d line(s)", len(fresh.Contacts))

		a.sayContactLinesTheDoorRefuses(*fresh)
		a.pokeOutbox()
	}
	if readAtUseChanged {
		logsink.Info("config.decision", "plugin settings, automatic updates, the page's switches and the relay connection's figures applied live (each is read where it is used)")
	}
	if togglesChanged {
		logsink.Info("config.decision", "tool toggles applied live -> disabled %v", fresh.Tools.Disabled)
	}
	if limitsChanged {
		_ = a.applyPluginLimits()
		logsink.Info("config.decision", "plugin limits applied live (plugins.runtime, plugins.resources): every plugin is judged again now, and each reads them at its next start")
	}
	if agencyChanged {
		ag, queue := fresh.Agency, "on"
		if !agencyOn(ag.SpawnQueue) {
			queue = "off"
		}
		logsink.Info("config.decision", "agency ceilings applied live -> depth %d, parallel %d (queue %s), mints %d, wall %ds, local wall %ds; a child %s rounds, %s calls, %s legs; a turn %s rounds, %s tokens, its checkpoint at %s calls undeclared or %d times a declared estimate, with %d rounds after, %s continuations in a row",
			ag.MaxSubagentDepth, ag.MaxParallelSubagents, queue, ag.SubagentMaxMints, ag.SubagentWallSeconds,
			ag.SubagentWallSecondsLocal, ag.SubagentMaxToolRounds, ag.SubagentMaxToolCalls, ag.SubagentMaxLegs, ag.MaxToolRounds, ag.TurnTokenBudget,
			checkpointCallsSetting.value(*fresh), slackSetting.n(*fresh), graceRoundsSetting.n(*fresh), continuationsSetting.value(*fresh))
	}
	if rhythmChanged {
		logsink.Info("config.decision", "metabolism rhythm re-armed live -> every %ds from now", fresh.Agency.RhythmSeconds)
	}
	if cognitionChanged {
		logsink.Info("config.decision", "the rhythm's settings applied live (Settings → Identity → Cognition): each facility reads them at its next pass")
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

func cognitionMoved(before, after Config) bool {
	for _, s := range cognitionSettings {
		if s.get(&before) != s.get(&after) {
			return true
		}
	}
	return before.Cognition != after.Cognition || before.Timezone != after.Timezone
}

func (a *App) applyAgency(cfg Config) {
	ag := cfg.Agency
	queue := agencyOn(ag.SpawnQueue)
	if a.engine != nil {

		a.engine.SetAgencyLimits(ag.MaxSubagentDepth, ag.MaxParallelSubagents, ag.SubagentMaxMints, ag.SubagentWallSeconds)
		a.engine.SetSpawnQueue(queue)
	}
	if a.store != nil {
		slots := 0
		if queue {
			slots = ag.MaxParallelSubagents
		}
		a.store.SetClaimLimit(identity.SubagentWorkKind, slots)
	}

	a.applyResidentLimits(a.residentLimitsFor(cfg))
}

func sameLLM(a, b LLMConfig) bool { return reflect.DeepEqual(a, b) }

func sameWritten(a, b Config) bool {
	x, err := json.Marshal(a)
	if err != nil {
		return false
	}
	y, err := json.Marshal(b)
	return err == nil && bytes.Equal(x, y)
}

func blankLiveAppliable(c *Config) {
	c.contactsPresent = false
	c.cognitionHeld = nil
	c.Logs.Level = ""
	c.Logs.Detail = nil
	c.Logs.Group = nil
	c.Logs.Taps = nil

	c.Memory.MeaningSource = ""
	c.LLM = LLMConfig{}
	c.Overrides = nil
	c.Speech = SpeechConfig{}
	c.Tools.ExtraRoots = nil
	c.Tools.SandboxOff = false
	c.Tools.Disabled = nil
	c.Dashboard.Files = DashboardFilesConfig{}
	c.Dashboard.Record = DashboardRecordConfig{}
	c.Dashboard.SentRequests = DashboardSentRequestsConfig{}
	c.Plugins.Autoload = ""
	c.Plugins.CatalogURL = ""
	c.Plugins.Grants = nil
	c.Plugins.AuthProfiles = nil

	c.Plugins.Runtime = PluginRuntimeConfig{SlowStorageCallMs: c.Plugins.Runtime.SlowStorageCallMs, MaxMemories: c.Plugins.Runtime.MaxMemories,
		MaxInflightReceipts: c.Plugins.Runtime.MaxInflightReceipts}
	c.Plugins.Resources = nil
	c.Maintenance = continuity.MaintenanceConfig{}
	c.Identity.DBOptimizeLevels = false
	c.Identity.DBLearnDictionary = false
	c.Plugins.Settings = nil
	c.Updates.Automatic = false
	c.Dashboard.Expert = false
	c.Dashboard.AccessCodeSeconds = 0
	c.Dashboard.RefusedPauses = 0
	c.Dashboard.UnsignedRequestsPerSecond, c.Dashboard.UnsignedRequestBurst = 0, 0
	c.Dashboard.Channels, c.Dashboard.ICEServers = nil, nil
	c.Dashboard.DirectWaitMS, c.Dashboard.DirectReadyAllowanceMS = 0, 0
	c.Dashboard.ChannelSessions, c.Dashboard.ChannelQueueBytes, c.Dashboard.ChannelStallMS = 0, 0, 0
	c.Dashboard.NoticesOffDashboard = false
	c.Contacts = nil
	c.Certificate.RouteMode = ""
	c.Certificate.RelayEndpoint = ""
	c.Certificate.DirectAddresses = nil
	c.Certificate.Prepare = nil
	c.Certificate.PrepareCertificate = false
	c.Certificate.RelayPingSeconds = 0
	c.Certificate.RelayBackoffBaseMS = 0
	c.Certificate.RelayBackoffMaxMS = 0
	c.Certificate.RelayAnswerSeconds = 0
	c.Certificate.RelayCarriedMax, c.Certificate.RelayAttachesPerSecond, c.Certificate.RelayAttachBurst = 0, 0, 0

	c.Agency = AgencyConfig{

		QueueWorkers: c.Agency.QueueWorkers,
	}

	c.Timezone = ""
	for _, s := range cognitionSettings {
		if s.card == "" {
			s.set(c, limits.Ceiling{})
		}
	}
	c.Cognition = CognitionConfig{}
	c.Dashboard.AccessToken = ""
	c.Dashboard.RequireToken = false
	c.Dashboard.ApprovedScripts = nil
}
