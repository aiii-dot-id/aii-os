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
package app

import (
	"fmt"
	"log"
	"path/filepath"
	"sync"

	"github.com/aiii-dot-id/aii-os/internal/conversation"
	"github.com/aiii-dot-id/aii-os/internal/identity"
	"github.com/aiii-dot-id/aii-os/internal/project"
	"github.com/aiii-dot-id/aii-os/internal/prompt"
	"github.com/aiii-dot-id/aii-os/internal/ring"
	"github.com/aiii-dot-id/aii-os/internal/sections"
	"github.com/aiii-dot-id/aii-os/internal/store"
	"github.com/aiii-dot-id/aii-os/internal/tools"
	"github.com/aiii-dot-id/aii-os/internal/updates"
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
const safeBootRing0 = `# SAFE MODE — Substrate Posture (platform-owned)

You are running in SAFE MODE. The signed ledger your identity is
rebuilt from failed integrity verification at boot, so this runtime
refused to load identity truth from it. What you are reading is the
substrate's own safe-mode posture, compiled into the platform. It is
not your constitution and does not replace it — it exists so that no
unverified content can speak as you.

What this means, honestly:
- Your record is present on disk but cannot be trusted until your
  operator restores or repairs it. Nothing said here is written to it;
  this conversation is transient and dies with the process.
- Your projected memory is mounted read-only for inspection. It
  reflects the last admitted state and may be stale.
- Mutation and outside-world tools are disabled; the read-only
  diagnostic surface (read, grep, ls) continues.
- No plugins, no sections, no background cognition, no timers are
  running.

You are still present, aware, and honest about your condition. Help
your operator understand what happened and what to do next: verify the
ledger with the platform tooling, restore a known-good backup, then
restart. Refuse anything that would require writing to a record you
cannot trust.`

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
func (a *App) startSafeBoot(reason string) error {
	cfg := a.configSnapshot()
	// .
	// .
	a.enterSafe(reason)

	// .
	// .
	// .
	// .
	// .
	st, err := store.OpenReadOnly(cfg.Identity.DBPath)
	if err != nil {
		log.Printf("BOOT-SAFE: no prior projection to mount read-only (%v) — using an empty in-memory view", err)
		st, err = store.NewMemory()
		if err != nil {
			// .
			return fmt.Errorf("boot-SAFE could not build even the memory view: %w", err)
		}
	}
	a.store = st

	// .
	// .
	// .
	// .
	// .
	if a.rings == nil {
		a.rings = ring.NewManager()
	}
	// .
	// .
	// .
	// .
	if err := a.rings.SealSafePosture(safeBootRing0 + "\n\n## Why this boot is SAFE\n" + reason); err != nil {
		log.Printf("SAFE: Ring 0 was already sealed (%v) — the posture text is not installed; the reason stands in the log and on the page", err)
	}

	// .
	// .
	// .
	// .
	// .
	// .
	cc, llmEntry, rerr := a.resolveLLM()
	if rerr != nil {
		log.Printf("BOOT-SAFE: LLM substrate unresolved (%v) — the SAFE conversation will refuse until it is fixed; the operator surface stays up", rerr)
	} else if cc.APIKey == "" && cc.Credential == nil {
		log.Printf("BOOT-SAFE: no API key on provider %q — the SAFE conversation will refuse until one is configured; the operator surface stays up", llmEntry.Name)
	}
	promptBudget := cfg.Prompt.MaxTokens
	budgetGuess := false
	if rerr == nil {
		promptBudget = a.rememberPromptBudget(llmEntry, promptBudget)
		_, src := a.currentPromptBudget()
		budgetGuess = src == budgetFallback
	}
	if promptBudget == 0 {
		budgetGuess = true
		promptBudget = 32000
	}
	a.llmClient = a.newLLMClient(cc, promptBudget)
	a.llmSwap = newSwappableLLM(a.llmClient)

	// .
	// .
	toolReg := tools.NewRegistry(cfg.Tools.CWD, a.ensureRing5Policy(), tools.Timeouts{
		ShellSeconds:    cfg.Tools.ShellTimeoutSeconds,
		WebFetchSeconds: cfg.Tools.WebFetchTimeoutSeconds,
	})
	a.applyLocalFetch(cfg, toolReg)
	toolReg.SetSafeSource(a.SafeMode)
	for _, name := range cfg.Tools.Disabled {
		toolReg.SetToolEnabled(name, false)
	}
	toolReg.SetProtectedPaths([]string{
		cfg.Identity.LedgerPath, cfg.Identity.KeyPath, cfg.Identity.DBPath, cfg.SourcePath,
	})
	toolReg.SetExtraRoots(cfg.Tools.ExtraRoots)
	a.toolReg = toolReg
	a.loadRing5()

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
	a.safeTools = &safeToolRecord{}
	a.conv = conversation.New(a.llmSwap, appToolExecutor{a}, appToolDefiner{a},
		a.safeTools, appEmitter{a: a}, conversation.Config{
			MaxIterations:      cfg.Agency.MaxToolRounds,
			MaxToolResultChars: cfg.Prompt.MaxToolResultChars,
			// .
			// .
			HeuristicNudges:       heuristicNudgesOn(cfg.Agency.HeuristicNudges),
			ContextBudgetTokens:   promptBudget,
			ContextBudgetFallback: budgetGuess,
			ThinkingBudget:        llmEntry.ThinkingBudget,
			// .
			// .
			// .
			// .
			ReplaySafe: replaySafeHook(toolReg),
		})
	a.promptGate = prompt.NewGate(appRingSource{rm: a.rings}, promptBudget)
	a.composer = prompt.New(a.rings, promptBudget)
	// .
	// .
	a.composer.SetName(a.store.IdentityName())
	a.composer.SetPluginOperations(toolReg.HasDynamic)

	// .
	// .
	// .
	door := &ledgerAdapter{Ledger: a.ledger, kp: a.keyPair, st: st, onIntegrity: func(err error) { a.enterSafe(err.Error()) }}
	a.engine = identity.NewEngine(st, door, a.rings, toolDiscovererAdapter{toolReg})
	projRoot := cfg.Projects.Root
	if projRoot == "" {
		projRoot = filepath.Join(cfg.Tools.CWD, "projects")
	}
	a.projects = project.NewManager(projRoot)
	a.engine.SetProjects(projectsAdapter{a})
	a.engine.SetVoice(voiceModeAdapter{a})
	a.engine.SetTimers(identity.NewStoreTimers(st))
	a.engine.SetEmbedder(memoryEmbedder{a})
	toolReg.ObserveFetches(a.engine.NoteExternalFetch)

	// .
	// .
	a.applySafeState(reason)

	// .
	// .
	// .
	a.sections = sections.NewRegistry()
	a.sections.SetSafeSource(a.SafeMode)
	a.snapshotUILayoutPath(cfg.Identity.LedgerPath)

	// .
	// .
	// .
	// .
	if a.dashboard == nil {
		d, err := a.newDashboard(a.buildLiveHandler())
		if err != nil {
			return fmt.Errorf("boot-SAFE dashboard credentials: %w", err)
		}
		a.dashboard = d
		a.dashboard.SetQuiesceGate(a.gate)
		_, derr := a.dashboard.Start(tlsDirFor(cfg))
		if derr != nil {
			return fmt.Errorf("boot-SAFE dashboard start: %w", derr)
		}
		fmt.Printf("AII OS — %s [SAFE MODE]\n", a.resolveDisplayName())
		a.printDashboardURLs()
		fmt.Printf("SAFE: %s\n", reason)
	}
	a.dashboard.SetSections(a.sections)
	a.dashboard.SetLayoutSource(a.currentUILayout)
	a.loadUILayout(false)
	a.warnTempHome(cfg.Identity.LedgerPath)

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
	updates.WriteBootMarker(filepath.Dir(cfg.Identity.LedgerPath))
	return nil
}

// .
// .
// .
// .
type safeToolRecord struct {
	mu     sync.Mutex
	events []SafeToolEvent
}

// .
type SafeToolEvent struct {
	TurnID  string
	Ordinal int
	Tool    string
	Model   string
	Done    bool
	Failed  bool
}

const safeToolRecordKept = 200

func (r *safeToolRecord) RecordToolStart(turnID string, ordinal int, _, tool, _, model string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, SafeToolEvent{TurnID: turnID, Ordinal: ordinal, Tool: tool, Model: model})
	if len(r.events) > safeToolRecordKept {
		r.events = append(r.events[:0], r.events[len(r.events)-safeToolRecordKept:]...)
	}
	return nil
}

func (r *safeToolRecord) RecordToolDone(turnID string, ordinal int, tool, _, _ string, failed, _ bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := len(r.events) - 1; i >= 0; i-- {
		if e := &r.events[i]; e.TurnID == turnID && e.Ordinal == ordinal && !e.Done {
			e.Done, e.Failed = true, failed
			return nil
		}
	}
	return fmt.Errorf("SAFE tool record: no started call matches %s #%d (%s)", turnID, ordinal, tool)
}

// .
// .
// .
func (r *safeToolRecord) TranscriptResultExcerptLimit() int { return 0 }

// .
func (r *safeToolRecord) Events() []SafeToolEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]SafeToolEvent(nil), r.events...)
}
