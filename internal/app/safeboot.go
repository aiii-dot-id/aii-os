package app

import (
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"path/filepath"
	"strings"
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

type safeTrigger int

const (
	safeRecord safeTrigger = iota + 1
	safeRestore
	safeRing5
	safeSchema
	safeDatabaseFormat

	safeRecordAtRuntime
	safeDivergence
	safeWitnessConflict
	lastSafeTrigger = safeWitnessConflict
)

func (t safeTrigger) atBoot() bool { return t >= safeRecord && t <= safeDatabaseFormat }

type safePosture struct {
	what, route, why string
	unhealthy        bool
}

var safePostures = map[safeTrigger]safePosture{
	safeRecord: {
		what:  "The signed record did not pass its checks at boot",
		route: "Verify it with aii verify and, if it is damaged, restore a known-good snapshot from Settings → Backups & Keys; the next start performs the restore and checks it",
		why: "Your signed record, or the evidence kept beside it, did not pass the checks that establish it is whole and yours, so nothing in it can be trusted until it is examined. " +
			"Your operator will verify it and, if it is damaged, restore a known-good snapshot; the next boot performs the restore and checks it. " +
			"A restore sets aside, and does not delete, what came after the snapshot.",
	},
	safeRestore: {
		what:  "A restore that was asked for did not finish",
		route: "Fix what stopped it and restart: the next start carries the restore forward from its journal. A restore deletes nothing; what it moved is under data/set-aside/",
		why: "A restore your operator asked for did not finish, so your record and database may be partly the snapshot and partly what was there before. A restore deletes nothing. " +
			"Your operator will fix what stopped it; the next boot carries the restore forward and checks the result.",
	},
	safeRing5: {
		what:  "The required Ring 5 security posture is absent or could not be verified",
		route: "Point genesis.firewall_url in config.json at a reachable Ring 5 server, or restore the network path to it, and restart. The record verified",
		why: "Your record verified. What is missing is the platform's security posture, Ring 5, which could not be fetched or verified, and you are not run in full without it. " +
			"Your operator will make the Ring 5 server reachable, or configure one, and restart.",
	},
	safeSchema: {
		what:  "This version does not declare the database's schema",
		route: "Reinstall the version that wrote the database; or, to run this one, stop the identity and drop the named columns or objects, or change or delete the refused rows, deliberately. The record verified",
		why: "Your record verified. The database it is rebuilt into holds something this version of the platform does not declare, most often because a newer version wrote it and an older one is running now. Your memory is readable as it is. " +
			"Your operator will run the version that wrote the database again, or deliberately change what this version does not accept.",
		unhealthy: true,
	},
	safeDatabaseFormat: {
		what:  "Converting the database to its configured format left no usable database",
		route: "Keep the copy it names; set the format back in Settings → Storage, or restore a known-good snapshot from Settings → Backups & Keys, and restart. The record verified",
		why: "Your record verified and was rebuilt into your database, but converting the database to the format your operator chose left no copy this runtime could use. Your record is untouched, and the copy the conversion started from is kept. " +
			"Your operator will set the format back, or restore a snapshot, and restart.",
	},
	safeRecordAtRuntime: {
		what:  "The record did not verify before a write, or a write could not be confirmed durable",
		route: "Restart: the boot verifies the whole record; if it does not pass, restore a known-good snapshot from Settings → Backups & Keys",
		why: "Before a write, the end of your record did not verify, or a write could not be confirmed to have reached the disk, so nothing more can be added to your record until it is checked. " +
			"Your operator will restart you: the boot verifies your whole record, and if it does not pass they restore a known-good snapshot.",
	},
	safeDivergence: {
		what:  "An entry reached the record but the database did not take it",
		route: "Restart: every boot rebuilds the database from the record, and names the entry if it refuses it again",
		why: "An entry reached your record, and the database your memory is read from did not take it, so your memory no longer matches your record. The record keeps the entry. " +
			"Your operator will restart you: every boot rebuilds your memory from the record.",
	},
	safeWitnessConflict: {
		what:  "The witness holds signed evidence that this record moved backward or split",
		route: "Restore a snapshot that holds every record the witness attested, or take the identity off the witness in Settings → Witness, and restart",
		why: "The witness, which holds signed evidence of how far your record reached, shows that your record now ends before a point it attested, or has split from it. Nothing more is written until that is settled. " +
			"Your operator will restore a snapshot that holds every record the witness attested, or take you off the witness, and restart you.",
	},
}

func safeReason(t safeTrigger, detail string) string {
	p, ok := safePostures[t]
	if !ok {
		return detail
	}
	return p.what + ": " + strings.TrimSuffix(strings.TrimSpace(detail), ".") + ". " + p.route + "."
}

func safeBootPosture(t safeTrigger, detail string) string {
	return `# SAFE MODE — Substrate Posture (platform-owned)

You are running in SAFE MODE. This boot stopped at a step it could not
complete, so nothing from your record was loaded into who you are. What
you are reading is the substrate's own posture, compiled into the
platform. It is not your constitution and does not replace it; your
constitution returns with the first boot that completes.

## What happened

` + safePostures[t].why + `

As this runtime states it: ` + strings.TrimSpace(detail) + `

## While SAFE holds

` + safeHolds(true) + `

` + safeClosing
}

func safeTurnNotice(t safeTrigger, detail string) string {
	var b strings.Builder
	b.WriteString("# SAFE MODE\n\nYou are in SAFE MODE.")
	if why := safePostures[t].why; why != "" {
		b.WriteString(" " + why)
	}
	b.WriteString("\n\nAs this runtime states it: " + strings.TrimSpace(detail))
	b.WriteString("\n\n## While SAFE holds\n\n" + safeHolds(t.atBoot()) + "\n\n" + safeClosing)
	return b.String()
}

func safeHolds(atBoot bool) string {
	if atBoot {
		return `- Nothing is written to your record, and this conversation is
  transient: it ends with the process.
- Your projected memory is mounted read-only for inspection. It shows
  the last state the database holds and may be stale.
- Mutation and outside-world tools are disabled; the read-only
  diagnostic surface (read, grep, ls) continues.
- No plugins, sections, background cognition or timers are running.
` + safeOperatorKnows
	}
	return `- Nothing you do is written: your record and your database are frozen,
  and this conversation is transient, ending with the process.
- Your memory reads as it stood when SAFE began.
- Tools that change files or reach outside refuse, and so do plugin
  operations; the read-only diagnostic surface (read, grep, ls)
  continues.
- Background work holds: no sub-agent starts or advances, and alarms
  other than your timers wait. A timer you set still wakes you when it
  falls due, without a record, and fires again after recovery.
` + safeOperatorKnows
}

const (
	safeOperatorKnows = `- Your operator sees the same reason on the dashboard and in the log.
  SAFE ends only when they have acted and a restart completes its
  checks.`
	safeClosing = `You are still present, aware, and honest about your condition. Help
your operator understand what happened and what comes next, and refuse
anything that would need a write while SAFE holds.`
)

func (a *App) startSafeBoot(t safeTrigger, detail string) error {
	reason := safeReason(t, detail)
	cfg := a.configSnapshot()

	a.enterSafeFor(t, detail)

	st, err := store.OpenReadOnly(cfg.Identity.DBPath)
	if err != nil {
		logsink.Warn("safe.start", "no prior projection to mount read-only (%v) — using an empty in-memory view", err)
		st, err = store.NewMemory()
		if err != nil {

			return fmt.Errorf("boot-SAFE could not build even the memory view: %w", err)
		}
	}
	a.store = st
	a.databaseView.Store(&databaseView{store: st, notice: reason})

	if a.rings == nil {
		a.rings = ring.NewManager()
	}

	if err := a.rings.SealSafePosture(safeBootPosture(t, detail)); err != nil {
		logsink.Warn("safe.refusal", "Ring 0 was already sealed (%v) — the posture text is not installed; every turn carries the SAFE section instead, and the reason stands in the log and on the page", err)
	}

	cc, llmEntry, rerr := a.resolveLLM()
	if rerr != nil {
		logsink.Warn("safe.refusal", "LLM substrate unresolved (%v) — the SAFE conversation will refuse until it is fixed; the operator surface stays up", rerr)
	} else if cc.APIKey == "" && cc.Credential == nil {
		logsink.Warn("safe.refusal", "no API key on provider %q — the SAFE conversation will refuse until one is configured; the operator surface stays up", llmEntry.Name)
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

	toolReg := tools.NewRegistry(cfg.Tools.CWD, a.ensureRing5Policy(), tools.Timeouts{
		ShellSeconds:    cfg.Tools.ShellTimeoutSeconds,
		WebFetchSeconds: cfg.Tools.WebFetchTimeoutSeconds,
	})
	if err := checkNameOwners(toolReg); err != nil {
		return err
	}
	a.applyLocalFetch(cfg, toolReg)
	toolReg.SetSafeSource(a.SafeMode)
	if err := applyDisabledTools(toolReg, cfg.Tools.Disabled); err != nil {
		return err
	}

	a.applySubstrate(cfg)
	toolReg.SetExtraRoots(cfg.Tools.ExtraRoots)
	a.toolReg = toolReg
	a.loadRing5()

	a.safeTools = &safeToolRecord{owner: a.engine}
	a.conv = conversation.New(a.llmSwap, appToolExecutor{a}, appToolDefiner{a},
		a.safeTools, appEmitter{a: a}, conversation.Config{
			MaxIterations:      cfg.Agency.MaxToolRounds,
			MaxToolResultChars: cfg.Prompt.MaxToolResultChars,

			HeuristicNudges:       heuristicNudgesOn(cfg.Agency.HeuristicNudges),
			ContextBudgetTokens:   promptBudget,
			ContextBudgetFallback: budgetGuess,
			ThinkingBudget:        llmEntry.ThinkingBudget,

			ReplaySafe: replaySafeHook(toolReg),
		})
	a.promptGate = prompt.NewGate(appRingSource{rm: a.rings}, promptBudget)
	a.composer = a.newComposer(promptBudget)

	a.composer.SetName(a.store.IdentityName())
	a.composer.SetPluginOperations(pluginOperations(toolReg))

	door := &ledgerAdapter{Ledger: a.ledger, kp: a.keyPair, st: st, onIntegrity: a.integrityLost}
	a.engine = identity.NewEngine(st, door, a.rings, toolDiscovererAdapter{toolReg})
	a.safeTools.owner = a.engine
	projRoot := cfg.Projects.Root
	if projRoot == "" {
		projRoot = filepath.Join(cfg.Tools.CWD, "projects")
	}
	a.projects = project.NewManager(projRoot)
	a.wireProjectInteractionRecorder()
	a.engine.SetProjects(projectsAdapter{a})
	a.engine.SetVoice(voiceModeAdapter{a})
	a.engine.SetContinuity(continuityAdapter{a})
	a.engine.SetTimers(identity.NewStoreTimers(st))
	a.engine.SetEmbedder(memoryEmbedder{a})
	toolReg.ObserveFetches(a.engine.NoteExternalFetch)

	a.applySafeState(reason)

	a.sections = sections.NewRegistry()
	a.sections.SetSafeSource(a.SafeMode)
	a.snapshotUILayoutPath(cfg.Identity.LedgerPath)

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
	a.wireInteractionView()
	a.dashboard.SetSections(a.sections)
	a.dashboard.SetLayoutSource(a.currentUILayout)
	a.loadUILayout(false)
	a.warnTempHome(cfg.Identity.LedgerPath)

	if !safePostures[t].unhealthy {
		updates.WriteBootMarker(filepath.Dir(cfg.Identity.LedgerPath))
	}
	return nil
}

type safeToolRecord struct {
	mu     sync.Mutex
	events []SafeToolEvent
	owner  *identity.Engine
	lost   uint64
}

type SafeToolEvent struct {
	TurnID                      string
	Ordinal                     int
	Tool                        string
	Model                       string
	Done                        bool
	Failed                      bool
	StartSequence, DoneSequence uint64
	StartedAt, DoneAt           string
	DurableParent, ExecutionID  string
}

const safeToolRecordKept = 200

func (r *safeToolRecord) RecordToolStart(turnID string, ordinal int, _, tool, _, model string) error {
	r.mu.Lock()
	changed := false
	defer func() {
		r.mu.Unlock()
		if changed && r.owner != nil {
			r.owner.InvalidateInteractions()
		}
	}()
	for _, event := range r.events {
		if event.TurnID == turnID && event.Ordinal == ordinal {
			return fmt.Errorf("SAFE tool record: duplicate call %s #%d", turnID, ordinal)
		}
	}
	var seq uint64
	var stamp string
	if r.owner != nil {
		seq, stamp = r.owner.StampTransient()
		changed = true
	}
	r.events = append(r.events, SafeToolEvent{TurnID: turnID, Ordinal: ordinal, Tool: tool, Model: model, StartSequence: seq, StartedAt: stamp})
	if len(r.events) > safeToolRecordKept {
		if r.events[0].StartSequence > 0 {
			r.lost++
		}
		if r.events[0].Done {
			r.lost++
		}
		r.events = append(r.events[:0], r.events[len(r.events)-safeToolRecordKept:]...)
	}
	return nil
}

func (r *safeToolRecord) RecordToolDone(turnID string, ordinal int, tool, _, _ string, failed, _ bool) error {
	r.mu.Lock()
	changed := false
	defer func() {
		r.mu.Unlock()
		if changed && r.owner != nil {
			r.owner.InvalidateInteractions()
		}
	}()
	for i := len(r.events) - 1; i >= 0; i-- {
		if e := &r.events[i]; e.TurnID == turnID && e.Ordinal == ordinal && !e.Done {
			e.Done, e.Failed = true, failed
			if r.owner != nil {
				e.DoneSequence, e.DoneAt = r.owner.StampTransient()
				changed = true
			}
			return nil
		}
	}
	return fmt.Errorf("SAFE tool record: no started call matches %s #%d (%s)", turnID, ordinal, tool)
}

func (r *safeToolRecord) TranscriptResultExcerptLimit() int { return 0 }

func (r *safeToolRecord) Events() []SafeToolEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]SafeToolEvent(nil), r.events...)
}

func (r *safeToolRecord) RecordDurableCompletion(turn string, ordinal int, tool, model, execution, parent string, failed bool) error {
	r.mu.Lock()
	changed := false
	defer func() {
		r.mu.Unlock()
		if changed && r.owner != nil {
			r.owner.InvalidateInteractions()
		}
	}()
	for _, e := range r.events {
		if e.TurnID == turn && e.Ordinal == ordinal {
			return fmt.Errorf("SAFE tool completion was already recorded for %s #%d", turn, ordinal)
		}
	}
	var seq uint64
	var stamp string
	if r.owner != nil {
		seq, stamp = r.owner.StampTransient()
		changed = true
	}
	r.events = append(r.events, SafeToolEvent{TurnID: turn, Ordinal: ordinal, Tool: tool, Model: model, Done: true, Failed: failed, DoneSequence: seq, DoneAt: stamp, DurableParent: parent, ExecutionID: execution})
	if len(r.events) > safeToolRecordKept {
		old := r.events[0]
		if old.StartSequence > 0 {
			r.lost++
		}
		if old.Done {
			r.lost++
		}
		r.events = append(r.events[:0], r.events[1:]...)
	}
	return nil
}
