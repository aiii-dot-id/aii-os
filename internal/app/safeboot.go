// Package main — boot-time SAFE: the minimal posture.
//
// startLive branches here when boot-time integrity verification fails:
// chain verification (the tampered-ledger case), or a projection
// rebuild the materializer refuses (a signed event that applies to
// nothing). Canon SAFE_MODE.md §4.1: "Mutable boot work stops as soon
// as that trigger is pending ... no database, ledger, checkpoint,
// cache, trace, audit, plugin, process, shell, or other filesystem
// mutation may begin"; §3.2 keeps the core interactive conversation and
// read-only identity access; SAFE_MODE_PLUGIN_LIFECYCLE.md §3.2: "On a
// boot that enters safe mode, do not launch or activate any plugin."
// Local law: R55 (no database writes while integrity is unverified),
// docs/SAFE_DEGRADED.md §2.5 (boot-SAFE vs run-SAFE).
//
// Mid-run SAFE (the onAppendErr funnel) is DIFFERENT by design: organs
// already running freeze in place — that path is untouched here.
package app

import (
	"fmt"
	"log"
	"path/filepath"

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

// safeBootRing0 is the PLATFORM-OWNED constitution of the SAFE posture:
// compiled into the binary, never read from the ledger that just failed
// verification. The tampered-constitution attack this closes: the old
// boot loaded Ring 0 from the REJECTED chain, so the very content
// verification refused governed the SAFE conversation. The identity in
// SAFE speaks from this substrate posture; their real constitution
// returns when their operator restores a verified record. The failure
// reason is appended per canon SAFE_MODE.md §3 ("every prompt frame
// carries the mode and the specific failure reason").
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

// startSafeBoot boots the minimal SAFE posture. Caller (startLive) has
// already loaded the key pair and opened the ledger; the reason names
// the integrity failure. What boots: the SAFE beacon, the dashboard
// (operator surface, sandbox settings, repair path), the conversation loop
// over a platform-owned Ring 0 + Ring 5, SAFE-gated tools, and a
// READ-ONLY mount of the prior projection (or an empty in-memory view
// when none exists). What does NOT: replay, durable store writes, Ring
// 0/1/2/3 from the rejected ledger, plugin/section activation, TIME,
// the executor, watchers, witness anchoring.
func (a *App) startSafeBoot(reason string) error {
	cfg := a.configSnapshot()
	// Freeze first (mode.go): the ledger is the single writer, so this
	// line alone makes "SAFE ⇒ no minting" true before any organ wires.
	a.enterSafe(reason)

	// READ-ONLY introspection (canon §3.2: "The last known-good
	// projection state (identity.db) remains queryable"): mount the
	// existing database with query_only on every connection — displayable,
	// unwritable. No prior database means an empty in-memory view: the
	// dashboard shows honest zeros and still creates NOTHING durable.
	st, err := store.OpenReadOnly(cfg.Identity.DBPath)
	if err != nil {
		log.Printf("BOOT-SAFE: no prior projection to mount read-only (%v) — using an empty in-memory view", err)
		st, err = store.NewMemory()
		if err != nil {
			// Canon §9 state D: halt if safe mode cannot start.
			return fmt.Errorf("boot-SAFE could not build even the memory view: %w", err)
		}
	}
	a.store = st

	// Rings: Ring 0 is the platform SAFE posture + the specific reason —
	// NEVER genesis.LoadRing0 (that reads the rejected chain). Ring 5 is
	// already platform-owned (bundle + local floor). Rings 1/2/3/4 stay
	// empty: charter, synthesis, and working truth all derive from the
	// record under suspicion.
	if a.rings == nil {
		a.rings = ring.NewManager()
	}
	a.rings.Set(ring.Ring0, &ring.RingContent{
		Level:   ring.Ring0,
		Content: safeBootRing0 + "\n\n## Why this boot is SAFE\n" + reason,
	})

	// The LLM client: SAFE keeps the conversation (canon §3.2 — the core
	// interactive provider lane bypasses the failed-integrity gates).
	// The substrate pointer resolves BEST-EFFORT here: a broken
	// providers.json, a dangling pointer, or a missing key must not
	// kill the SAFE surface — the chat errors honestly at call time
	// instead, and the operator surface stays up to fix it.
	cc, llmEntry, rerr := a.resolveLLM()
	if rerr != nil {
		log.Printf("BOOT-SAFE: LLM substrate unresolved (%v) — the SAFE conversation will refuse until it is fixed; the operator surface stays up", rerr)
	} else if cc.APIKey == "" && cc.Credential == nil {
		log.Printf("BOOT-SAFE: no API key on provider %q — the SAFE conversation will refuse until one is configured; the operator surface stays up", llmEntry.Name)
	}
	promptBudget := cfg.Prompt.MaxTokens
	if rerr == nil {
		promptBudget = promptBudgetFor(llmEntry, promptBudget)
	}
	if promptBudget == 0 {
		promptBudget = 32000
	}
	a.llmClient = a.newLLMClient(cc, promptBudget)
	a.llmSwap = newSwappableLLM(a.llmClient)

	// SAFE-gated tools: canon §3.3.1 — mutation and outside-world tools
	// refuse with the reason; the read-only diagnostic surface continues.
	toolReg := tools.NewRegistry(cfg.Tools.CWD, a.ensureRing5Policy(), tools.Timeouts{
		ShellSeconds:    cfg.Tools.ShellTimeoutSeconds,
		WebFetchSeconds: cfg.Tools.WebFetchTimeoutSeconds,
	})
	toolReg.SetSafeSource(a.SafeMode)
	for _, name := range cfg.Tools.Disabled {
		toolReg.SetToolEnabled(name, false)
	}
	toolReg.SetProtectedPaths([]string{
		cfg.Identity.LedgerPath, cfg.Identity.KeyPath, cfg.Identity.DBPath, cfg.SourcePath,
	})
	toolReg.SetExtraRoots(cfg.Tools.ExtraRoots)
	a.toolReg = toolReg
	a.loadRing5() // absent stays absent; SAFE never fabricates Ring 5

	// The conversation loop — the person is alive; the record is frozen.
	a.conv = conversation.New(a.llmSwap, appToolExecutor{a}, appToolDefiner{a},
		appTranscript{st}, appEmitter{a}, conversation.Config{
			MaxIterations:       cfg.Agency.MaxToolRounds,
			MaxToolResultChars:  cfg.Prompt.MaxToolResultChars,
			ContextBudgetTokens: promptBudget,
			ThinkingBudget:      llmEntry.ThinkingBudget,
		})
	a.promptGate = prompt.NewGate(appRingSource{a.rings}, promptBudget)
	a.composer = prompt.New(a.rings, promptBudget)
	// The name is display truth, safe to read from the prior projection
	// (a name shown is not authority loaded).
	name := a.store.IdentityName()
	if name == "" {
		name = cfg.Identity.Name
	}
	a.composer.SetName(name)

	// The engine: transient transcript, refusing verbs (its SAFE state
	// lands via applySafeState below). The read-only store underneath is
	// the second wall — even a missed gate cannot write.
	door := &ledgerAdapter{Ledger: a.ledger, kp: a.keyPair, st: st, onIntegrity: func(err error) { a.enterSafe(err.Error()) }}
	a.engine = identity.NewEngine(st, door, a.rings, toolDiscovererAdapter{toolReg})
	projRoot := cfg.Projects.Root
	if projRoot == "" {
		projRoot = filepath.Join(cfg.Tools.CWD, "projects")
	}
	a.projects = project.NewManager(projRoot) // lazy: creates nothing until used
	a.engine.SetProjects(projectsAdapter{a})
	a.engine.SetTimers(identity.NewStoreTimers(st))
	toolReg.ObserveFetches(a.engine.NoteExternalFetch)

	// Push SAFE onto the organs that now exist (the S1 second half —
	// enterSafe ran before they were wired).
	a.applySafeState(reason)

	// Sections: an EMPTY registry with the SAFE source — the dashboard
	// asks, the registry answers "nothing, because SAFE". No activation
	// (lifecycle canon §3.2), no dev-serve.
	a.sections = sections.NewRegistry()
	a.sections.SetSafeSource(a.SafeMode)
	a.snapshotUILayoutPath(cfg.Identity.LedgerPath)

	// The operator surface: dashboard, sandbox settings, and repair path.
	// The full live handler runs over the read-only store — reads
	// display the prior admitted state; any mutation surface fails with
	// the honest read-only error.
	if a.dashboard == nil {
		a.dashboard = a.newDashboard(a.buildLiveHandler())
		a.dashboard.SetQuiesceGate(a.gate)
		_, derr := a.dashboard.Start(tlsDirFor(cfg))
		if derr != nil {
			return fmt.Errorf("boot-SAFE dashboard start: %w", derr)
		}
		fmt.Printf("AII OS — %s [SAFE MODE]\n", name)
		fmt.Printf("Dashboard: %s\n", a.dashboard.Origin())
		fmt.Printf("SAFE: %s\n", reason)
	}
	a.dashboard.SetSections(a.sections)
	a.dashboard.SetLayoutSource(a.currentUILayout)
	a.loadUILayout(false) // a pure read of ui-layout.json
	a.warnTempHome(cfg.Identity.LedgerPath)

	// Deliberately ABSENT (the point of the minimal posture): ledger
	// replay; TIME (its legacy-alarm cleanup mutates durable alarms);
	// the executor and work-queue polling; cognition (DREAM/CONSOLIDATE/
	// SELF_MODEL wiring); witness anchoring; config/layout watchers and
	// SIGHUP reload; plugin and section activation. a.live stays false —
	// this runtime is up to be repaired, not to accumulate life.

	// Boot-health marker (R70): SAFE is a valid boot — the binary works,
	// it just can't trust the ledger. Without this, a new binary that
	// enters SAFE on boot would look like a failed boot and trigger
	// false rollback.
	updates.WriteBootMarker(filepath.Dir(cfg.Identity.LedgerPath))
	return nil
}
