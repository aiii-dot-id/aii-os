package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/cognitive"
	"github.com/aiii-dot-id/aii-os/internal/conversation"
	"github.com/aiii-dot-id/aii-os/internal/identity"
	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// alarmEnqueuerAdapter: TIME dispatch → durable work items, THROUGH
// the executor door — Enqueue pokes the loop awake, and since the
// executor ticker became a 60s heartbeat (2026-08-26) the poke is the
// delivery path, not an optimization.
type alarmEnqueuerAdapter struct {
	ex *cognitive.Executor
}

// alarmLeaseMs: the lease one alarm firing rides. THE LEASE MUST OUTLIVE
// THE RUN — the same lesson identity/work.go records for sub-agents
// (Method review, F2): under the store's 300s default, a facility pass
// that runs longer is swept back to PENDING by a sibling worker's crash
// recovery, and a SECOND CONCURRENT copy runs the same pass over the same
// files. Alarm handlers have no wall cap to derive a bound from, so it is
// named here: a facility pass is a handful of LLM calls at llm.timeout
// (120s each by default) plus, for maintenance, a whole-ledger copy and
// walk. A lease is CRASH RECOVERY, not a scheduler — too long only delays
// re-driving a firing whose process really died; too short manufactures
// the duplicate run.
const alarmLeaseMs = int64(30 * time.Minute / time.Millisecond)

func (a alarmEnqueuerAdapter) EnqueueAlarm(alarm store.Alarm) error {
	// repeat_every TRAVELS WITH THE FIRING. Leaving it out did not lose
	// a detail — it changed what the alarm IS. TIME's transition switch
	// reads this field to decide how a firing settles, so a repeating
	// alarm arrived at the handler looking one-shot, and both settlements
	// were then wrong in opposite directions: an ACCEPTED pass deleted the
	// alarm instead of re-arming it, and a DECLINED pass kept its stale
	// past deadline, which is due again instantly — 1,596 work items for a
	// single rhythm firing, for the length of one turn.
	payload, _ := json.Marshal(map[string]interface{}{
		"alarm_id":     alarm.AlarmID,
		"owner":        alarm.OwnerName,
		"clock":        alarm.Clock,
		"deadline":     alarm.Deadline,
		"repeat_every": alarm.RepeatEvery,
		"payload":      alarm.Payload,
	})
	// Dedup: the same firing (id+deadline) enqueues once; a NEW firing
	// (deadline moved) is new work.
	//
	// THROUGH THE EXECUTOR, not the store (2026-08-26): the executor
	// ticker is a 60s heartbeat now and Enqueue is the door that pokes
	// the loop awake. This adapter went to the store directly and the
	// timer-wake sequence test failed the day the heartbeat stretched —
	// an alarm waiting up to a minute for a poll is a broken promise.
	_, err := a.ex.Enqueue(
		"alarm."+alarm.OwnerName,
		string(payload),
		fmt.Sprintf("%s@%d", alarm.AlarmID, alarm.Deadline),
		"time",
		1, // alarms are user-facing promises — high, deliberate
		0,
		alarmLeaseMs,
	)
	return err
}

// alarmHandler runs alarm work through the registered owners — the
// same dispatch law, under the executor's panic containment.
type alarmHandler struct {
	time  ownerLookup
	clock interface {
		ApplyTransitions(alarm store.Alarm) // TIME's CAS transitions
	}
}

// ownerLookup is the surface the handler needs from TIME.
type ownerLookup interface {
	OwnerFor(name string) (cognitive.AlarmOwner, bool)
	InvokeAlarmOwner(ctx context.Context, owner cognitive.AlarmOwner, alarm store.Alarm) cognitive.AlarmResult
	ApplyAlarmTransitions(alarm store.Alarm, result cognitive.AlarmResult) error
	ClearPendingDispatch(alarmID string)
}

func (h *alarmHandler) WorkKinds() []string { return []string{"alarm.*"} }

func (h *alarmHandler) RunWork(ctx context.Context, w *store.WorkItem) error {
	var p struct {
		AlarmID     string `json:"alarm_id"`
		Owner       string `json:"owner"`
		Clock       string `json:"clock"`
		Deadline    int64  `json:"deadline"`
		RepeatEvery *int64 `json:"repeat_every"`
		Payload     string `json:"payload"`
	}
	if err := json.Unmarshal([]byte(w.Payload), &p); err != nil {
		// Dying before invocation: release the in-flight hold NOW, not
		// on the 30s expiry — the row retries without the dead weight.
		h.time.ClearPendingDispatch(p.AlarmID)
		return fmt.Errorf("alarm payload: %w", err)
	}
	alarm := store.Alarm{
		AlarmID: p.AlarmID, OwnerName: p.Owner, Clock: p.Clock,
		Deadline: p.Deadline, RepeatEvery: p.RepeatEvery, Payload: p.Payload,
	}
	owner, ok := h.time.OwnerFor(p.Owner)
	if !ok {
		h.time.ClearPendingDispatch(p.AlarmID)
		return fmt.Errorf("unregistered owner %q (alarm %s preserved)", p.Owner, p.AlarmID)
	}
	result := h.time.InvokeAlarmOwner(ctx, owner, alarm)
	// The settling is part of the work, not a postscript to it. Returning
	// nil regardless marked the item DONE while the alarm row still held
	// its old deadline — the queue said finished, the clock disagreed,
	// and the only trace was a log line.
	return h.time.ApplyAlarmTransitions(alarm, result)
}

// subagentHandler runs spawned sub-goals (2026-08-18 agency ruling):
// the spawned run IS the identity — same conversation loop, same tools,
// same ring-gated prompt — on one goal, bounded by operator config.
// Everything about it is Ring 4 ephemeral: the outcome DELIVERS into
// the work session and surfaces in working state; nothing mints unless
// the identity itself notes it (a sub-goal is not identity truth).
type subagentHandler struct{ a *App }

func (h *subagentHandler) WorkKinds() []string { return []string{identity.SubagentWorkKind} }

func (h *subagentHandler) RunWork(ctx context.Context, w *store.WorkItem) error {
	var p identity.SubagentRequest
	if err := json.Unmarshal([]byte(w.Payload), &p); err != nil {
		return fmt.Errorf("subagent payload: %w", err)
	}
	if err := p.Validate(); err != nil {
		return fmt.Errorf("subagent payload: %w", err)
	}

	// Effort ≤ EFFECTIVE parent (the identity's ruling, per THREAD): a
	// directed hand's current level is its directed one — a child clamps
	// against the spawner's effective budget, propagated down the tree,
	// never against the global config (which only bounds the main thread).
	limit := p.ParentBudget
	if limit <= 0 {
		// Spawned from the main thread: the ceiling is the RESOLVED
		// substrate's thinking budget (provider data lives on the
		// providers.json entry now). Unresolved = 0, the same as no
		// budget configured.
		if _, entry, err := h.a.resolveLLM(); err == nil {
			limit = entry.ThinkingBudget
		}
	}
	cfg := h.a.configSnapshot()
	thinking := limit
	if p.ThinkingBudget > 0 && p.ThinkingBudget < limit {
		thinking = p.ThinkingBudget
	}

	wall := time.Duration(cfg.Agency.SubagentWallSeconds) * time.Second
	runCtx, cancel := context.WithTimeout(ctx, wall)
	defer cancel()
	// Depth and effective budget are engine-stamped from THIS context on
	// any nested spawn — the downward direction propagates per thread.
	runCtx = context.WithValue(runCtx, identity.SubagentDepth{}, p.Depth)
	runCtx = context.WithValue(runCtx, identity.SubagentBudget{}, thinking)
	runCtx = context.WithValue(runCtx, identity.SubagentWorkSession{}, p.SessionID)
	mintCount := 0
	runCtx = context.WithValue(runCtx, identity.SubagentMints{}, &mintCount)

	h.a.emitToolEvent("subagent", "spawn", fmt.Sprintf("depth %d: %s", p.Depth, p.Goal))

	goalMsg := llm.Message{Role: "user", Content: buildSubagentGoal(p.Depth, p.Goal)}
	target := h.a.resolveRunTarget(p.Role)
	reserve, err := h.a.promptReserve(goalMsg, 0)
	if err != nil {
		startErr := fmt.Errorf("subagent request estimate: %w", err)
		if deliveryErr := h.a.store.DeliverWorkSession(p.SessionID, "failed before start: "+err.Error()); deliveryErr != nil {
			startErr = errors.Join(startErr, fmt.Errorf("record subagent start failure: %w", deliveryErr))
		}
		return startErr
	}
	composeFn := h.a.composer.ComposeFoldedWithin // default: identity whole, working truth as routes
	if p.Context == "full" {
		composeFn = h.a.composer.ComposeWithin
	}
	prompt, err := composeFn(target.budget, "", reserve)
	if err != nil {
		startErr := fmt.Errorf("subagent compose: %w", err)
		if deliveryErr := h.a.store.DeliverWorkSession(p.SessionID, "failed before start: "+err.Error()); deliveryErr != nil {
			startErr = errors.Join(startErr, fmt.Errorf("record subagent start failure: %w", deliveryErr))
		}
		return startErr
	}
	// A derived loop carries the clamped thinking budget; everything
	// else — tools, transcript, emitter, rounds — is the identity's own.
	subLoop := conversation.New(target.client, appToolExecutor{h.a}, appToolDefiner{h.a},
		appTranscript{h.a.store}, appEmitter{h.a}, conversation.Config{
			MaxIterations:       cfg.Agency.MaxToolRounds,
			MaxToolResultChars:  cfg.Prompt.MaxToolResultChars,
			ContextBudgetTokens: target.budget,
			ThinkingBudget:      thinking,
		})
	result, err := subLoop.RunSystem(runCtx, h.a.gatedSystem(prompt), []llm.Message{goalMsg}, 0)
	outcome := ""
	if err != nil {
		// Honest delivery either way — a failed sub-goal is still an
		// outcome the identity should see in its working state.
		outcome = "FAILED: " + err.Error()
	} else {
		outcome = result.Spoken
		if outcome == "" {
			outcome = "(completed with no spoken outcome)"
		}
	}
	modelID := result.ModelID
	if modelID == "" {
		modelID = target.modelID
	}
	tokens := fmt.Sprintf("%d", result.Usage.TotalTokens)
	if !result.Usage.Complete() {
		tokens = ">=" + tokens
	}
	outcome = fmt.Sprintf("[sub-agent role=%q model=%q fallback=%t calls=%d tokens=%s]\n%s",
		p.Role, modelID, target.fallback, result.Usage.Calls, tokens, outcome)
	if deliveryErr := h.a.store.DeliverWorkSession(p.SessionID, outcome); deliveryErr != nil {
		if err != nil {
			return errors.Join(fmt.Errorf("subagent run: %w", err), fmt.Errorf("deliver subagent outcome: %w", deliveryErr))
		}
		return fmt.Errorf("deliver subagent outcome: %w", deliveryErr)
	}
	h.a.emitToolEvent("subagent", "done", fmt.Sprintf("%s: %s", p.SessionID, compactText(outcome, 120)))
	if h.a.dashboard != nil {
		h.a.dashboard.PokeOutbox()
	}
	// Operator visibility (outbox is runtime plumbing, not identity truth).
	if oerr := h.a.store.AddOutboxMessage("subagent_"+p.SessionID, "operator", "",
		fmt.Sprintf("[sub-agent done] %s → %s", p.Goal, compactText(outcome, 400)), nil); oerr != nil {
		log.Printf("SUBAGENT %s: outbox notice failed: %v", p.SessionID, oerr)
	}
	if err != nil {
		return fmt.Errorf("subagent run: %w", err)
	}
	return nil
}

func compactText(s string, limit int) string {
	runes := []rune(s)
	if limit <= 0 || len(runes) <= limit {
		return s
	}
	if limit < 5 {
		return string(runes[:limit])
	}
	tail := limit / 4
	head := limit - tail - 1
	return string(runes[:head]) + "…" + string(runes[len(runes)-tail:])
}

func buildSubagentGoal(depth int, goal string) string {
	return fmt.Sprintf("[sub-agent, depth %d] Your sub-goal: %s\n\n"+
		"Work it to completion with your tools. Your final message is delivered "+
		"back to your working state.", depth, goal)
}

type runTarget struct {
	client   conversation.LLMClient
	budget   int
	modelID  string
	fallback bool
}

func (a *App) activeRunTarget(fallback bool) runTarget {
	a.cfgMu.RLock()
	defer a.cfgMu.RUnlock()
	client := a.llmSwap.Current()
	return runTarget{client: client, budget: a.composer.MaxTokens(), modelID: client.ModelName(), fallback: fallback}
}

// resolveRunTarget binds one run to one client and prompt budget before
// composing. Role is only a route key: invalid or unavailable routes
// fall back to the active model and never change the work protocol.
func (a *App) resolveRunTarget(role string) runTarget {
	if role == "" {
		return a.activeRunTarget(false)
	}
	cfg := a.configSnapshot()
	reg, regErr := a.loadProviders()

	// Precedence is the doctrine's (ruled 2026-08-26): an EXPLICIT
	// route wins; then THE CHECKBOX with a live local entry; then the
	// configured model. Fallback logs, never refuses — and untagged
	// spawns never reach this function at all: identity copies think
	// with the identity's own model.
	if route, ok := cfg.Agency.Roles[role]; ok {
		if regErr != nil {
			log.Printf("subagent role %q: providers unavailable (%v) — using the active model", role, regErr)
			return a.activeRunTarget(true)
		}
		cc, entry, err := a.resolveLLMConfig(LLMConfig{Provider: route.Provider, Model: route.Model, APIKeyEnv: cfg.LLM.APIKeyEnv}, reg)
		if err != nil {
			log.Printf("subagent role %q: route %s/%s did not resolve (%v) — using the active model", role, route.Provider, route.Model, err)
			return a.activeRunTarget(true)
		}
		budget := promptBudgetFor(entry, cfg.Prompt.MaxTokens)
		log.Printf("subagent role %q routed: provider %q model %q (prompt budget %d)", role, entry.Name, cc.Model, budget)
		return runTarget{client: a.newLLMClient(cc, budget), budget: budget, modelID: cc.Model}
	}

	if cfg.Agency.PreferLocalForRoles && regErr == nil {
		if local, ok := localProviderEntry(reg); ok &&
			a.probeProviders(&providerRegistry{Providers: []providerEntry{local}})[local.Name].state == "ok" {
			cc, entry, err := a.resolveLLMConfig(LLMConfig{Provider: local.Name, APIKeyEnv: cfg.LLM.APIKeyEnv}, reg)
			if err == nil {
				budget := promptBudgetFor(entry, cfg.Prompt.MaxTokens)
				log.Printf("subagent role %q → local model %q on %q (checkbox; prompt budget %d)", role, cc.Model, entry.Name, budget)
				return runTarget{client: a.newLLMClient(cc, budget), budget: budget, modelID: cc.Model}
			}
			log.Printf("subagent role %q: local entry %q did not resolve (%v) — using the active model", role, local.Name, err)
		}
	}

	log.Printf("subagent role %q: no route — using the active model", role)
	return a.activeRunTarget(true)
}

// localProviderEntry finds the operator's marked local entry — the
// FIRST "local": true wins; more than one marked is an operator
// choice this code does not referee.
func localProviderEntry(reg *providerRegistry) (providerEntry, bool) {
	for _, e := range reg.Providers {
		if e.Local {
			return e, true
		}
	}
	return providerEntry{}, false
}
