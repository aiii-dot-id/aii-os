package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"encoding/json"
	"github.com/aiii-dot-id/aii-os/internal/cognitive"
	"github.com/aiii-dot-id/aii-os/internal/conversation"
	"github.com/aiii-dot-id/aii-os/internal/identity"
	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// .
// .
// .
// .
type alarmEnqueuerAdapter struct {
	ex *cognitive.Executor
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
const alarmLeaseMs = int64(30 * time.Minute / time.Millisecond)

func (a alarmEnqueuerAdapter) EnqueueAlarm(alarm store.Alarm) error {
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	payload, _ := json.Marshal(map[string]interface{}{
		"alarm_id":     alarm.AlarmID,
		"owner":        alarm.OwnerName,
		"clock":        alarm.Clock,
		"deadline":     alarm.Deadline,
		"repeat_every": alarm.RepeatEvery,
		"payload":      alarm.Payload,
	})
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	_, err := a.ex.Enqueue(
		"alarm."+alarm.OwnerName,
		string(payload),
		fmt.Sprintf("%s@%d", alarm.AlarmID, alarm.Deadline),
		"time",
		1,
		0,
		alarmLeaseMs,
	)
	return err
}

// .
// .
type alarmHandler struct {
	time ownerLookup
}

// .
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
		// .
		// .
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
	// .
	// .
	// .
	// .
	return h.time.ApplyAlarmTransitions(alarm, result)
}

// .
// .
// .
// .
// .
// .
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

	// .
	// .
	// .
	// .
	limit := p.ParentBudget
	if limit <= 0 {
		// .
		// .
		// .
		// .
		if _, entry, err := h.a.resolveLLM(); err == nil {
			limit = entry.ThinkingBudget
		}
	}
	cfg := h.a.configSnapshot()
	leg := p.Leg
	if leg < 1 {
		leg = 1
	}
	maxLegs := cfg.Agency.SubagentMaxLegs
	if maxLegs < 1 {
		maxLegs = 1
	}
	legsOn := agencyOn(cfg.Agency.SubagentContinuation)
	// .
	// .
	// .
	// .
	// .
	if leg > 1 && h.sessionDelivered(p.SessionID) {
		log.Printf("SUBAGENT %s: leg %d not run — the session was delivered in an earlier leg", p.SessionID, leg)
		return nil
	}
	thinking := limit
	if p.ThinkingBudget > 0 && p.ThinkingBudget < limit {
		thinking = p.ThinkingBudget
	}

	// .
	// .
	// .
	// .
	// .
	wallSeconds := p.WallSeconds
	if wallSeconds <= 0 {
		wallSeconds = cfg.Agency.SubagentWallSeconds
	}
	wall := time.Duration(wallSeconds) * time.Second
	runCtx, cancel := context.WithTimeout(ctx, wall)
	defer cancel()
	// .
	// .
	runCtx = context.WithValue(runCtx, identity.SubagentDepth{}, p.Depth)
	runCtx = context.WithValue(runCtx, identity.SubagentBudget{}, thinking)
	runCtx = context.WithValue(runCtx, identity.SubagentWorkSession{}, p.SessionID)
	mintCount := 0
	runCtx = context.WithValue(runCtx, identity.SubagentMints{}, &mintCount)

	h.a.emitToolEvent("subagent", "spawn", fmt.Sprintf("depth %d: %s", p.Depth, p.Goal))

	goalText := buildSubagentGoal(p.Depth, p.Goal, cfg.Agency.SubagentMaxToolRounds, cfg.Agency.SubagentMaxToolCalls, maxLegs, wallSeconds)
	if leg > 1 {
		ws, werr := h.a.store.WorkSessionByID(p.SessionID)
		if werr != nil {
			log.Printf("SUBAGENT %s: leg %d could not read its session: %v", p.SessionID, leg, werr)
		}
		goalText += "\n\n" + continuationPreface(leg, maxLegs, ws)
	}
	goalMsg := llm.Message{Role: "user", Content: goalText}
	target := h.a.resolveRunTarget(p.Role)
	// .
	// .
	// .
	h.a.emitPluginEvent(pluginhost.TopicSubagentSpawned, map[string]interface{}{"session": p.SessionID, "role": p.Role, "model": target.modelID, "depth": p.Depth, "leg": leg})
	reserve, err := h.a.promptReserve(goalMsg, 0)
	if err != nil {
		startErr := fmt.Errorf("subagent request estimate: %w", err)
		if deliveryErr := h.a.store.DeliverWorkSession(p.SessionID, "unserved: failed before start: "+err.Error(), store.EvidenceNotRun, ""); deliveryErr != nil {
			startErr = errors.Join(startErr, fmt.Errorf("record subagent start failure: %w", deliveryErr))
		}
		return startErr
	}
	composeFn := h.a.composer.ComposeFoldedWithin
	if p.Context == "full" {
		composeFn = h.a.composer.ComposeWithin
	}
	prompt, err := composeFn(target.budget, "", reserve)
	if err != nil {
		startErr := fmt.Errorf("subagent compose: %w", err)
		if deliveryErr := h.a.store.DeliverWorkSession(p.SessionID, "unserved: failed before start: "+err.Error(), store.EvidenceNotRun, ""); deliveryErr != nil {
			startErr = errors.Join(startErr, fmt.Errorf("record subagent start failure: %w", deliveryErr))
		}
		return startErr
	}
	// .
	// .
	// .
	// .
	// .
	var predictedNow func() int
	if legsOn {
		meter, release := h.a.registerLegMeter(p.SessionID)
		defer release()
		predictedNow = meter.get
	}
	// .
	// .
	subLoop := conversation.New(target.client, appToolExecutor{h.a}, appToolDefiner{h.a},
		appTranscript{st: h.a.store, actor: p.SessionID}, appEmitter{a: h.a, actor: p.SessionID}, conversation.Config{
			// .
			// .
			// .
			MaxIterations: cfg.Agency.SubagentMaxToolRounds,
			// .
			// .
			// .
			MaxToolCalls:       cfg.Agency.SubagentMaxToolCalls,
			MaxToolResultChars: cfg.Prompt.MaxToolResultChars,
			// .
			// .
			HeuristicNudges:     heuristicNudgesOn(cfg.Agency.HeuristicNudges),
			ContextBudgetTokens: target.budget,
			ThinkingBudget:      thinking,
			BreadthNudge:        cfg.Agency.BreadthNudge,
			PredictedThisTurn:   predictedNow,
		})
	// .
	// .
	if h.a.dashboard != nil {
		h.a.dashboard.BroadcastWork()
	}
	runStart := time.Now()
	result, err := subLoop.RunSystem(runCtx, h.a.gatedSystem(prompt), []llm.Message{goalMsg}, 0)
	runWall := time.Since(runStart)
	accCalls := p.AccCalls + result.ToolCallsUsed
	accTokens := p.AccTokens + result.Usage.TotalTokens
	accWall := p.AccWallMs + runWall.Milliseconds()
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	if delivered, ok := h.deliveredResult(p.SessionID); ok {
		log.Printf("SUBAGENT %s: leg %d delivered by the child itself after %d calls — chain closed", p.SessionID, leg, result.ToolCallsUsed)
		if err != nil {
			log.Printf("SUBAGENT %s: run ended in error after its own delivery (the delivered result stands): %v", p.SessionID, err)
		}
		h.closeChild(p, leg, accCalls, accTokens, accWall, modelIDOf(result, target), err, delivered, fmt.Sprintf("delivered by the child (leg %d)", leg))
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
	bounded := err == nil && (result.ContinuedAtCap || result.ExhaustedBudget || result.ExhaustedCallBudget)
	if bounded && legsOn && leg < maxLegs {
		if merr := h.a.store.InsertSubagentMetric(store.SubagentMetric{
			SessionID: p.SessionID, TsMs: time.Now().UTC().UnixMilli(),
			Calls: accCalls, Tokens: accTokens, WallMs: accWall, Failed: false,
			Role: p.Role, Model: modelIDOf(result, target), Legs: leg,
		}); merr != nil {
			log.Printf("SUBAGENT %s: leg %d metric not recorded: %v", p.SessionID, leg, merr)
		}
		next := p
		next.Leg, next.AccCalls, next.AccTokens, next.AccWallMs = leg+1, accCalls, accTokens, accWall
		next.WallSeconds = wallSeconds
		payload, perr := json.Marshal(next)
		if perr != nil {
			log.Printf("SUBAGENT %s: leg %d could not encode its continuation: %v — delivering unfinished", p.SessionID, leg, perr)
		} else if _, qerr := h.a.store.EnqueueWork(&store.WorkItem{
			Kind: identity.SubagentWorkKind, Payload: string(payload),
			DedupKey: fmt.Sprintf("%s#%d", p.SessionID, leg+1), Source: "identity",
			LeaseMs: int64(wallSeconds+60) * 1000,
		}); qerr != nil {
			log.Printf("SUBAGENT %s: leg %d could not enqueue leg %d: %v — delivering unfinished", p.SessionID, leg, leg+1, qerr)
		} else {
			why := "declared budget"
			switch {
			case result.ExhaustedCallBudget:
				why = "call ceiling"
			case result.ExhaustedBudget:
				why = "round ceiling"
			}
			log.Printf("SUBAGENT %s: leg %d of %d ended at its %s after %d calls — leg %d enqueued", p.SessionID, leg, maxLegs, why, result.ToolCallsUsed, leg+1)
			h.a.emitToolEvent("subagent", "continue", fmt.Sprintf("%s: leg %d → %d (%s)", p.SessionID, leg, leg+1, why))
			if h.a.dashboard != nil {
				h.a.dashboard.BroadcastWork()
			}
			if h.a.queueWake != nil {
				h.a.queueWake()
			}
			return nil
		}
	}
	// .
	// .
	// .
	// .
	// .
	// .
	body := result.Spoken
	if body == "" {
		body = "(completed with no spoken outcome)"
	}
	outcome := ""
	switch {
	case err != nil:
		// .
		// .
		outcome = "unserved: FAILED: " + err.Error()
	case result.ExhaustedBudget:
		outcome = "partial: UNFINISHED — the sub-agent reached its round ceiling before it was done. What follows is real but partial; decide whether to spawn a continuation.\n\n" + body
	case result.ExhaustedCallBudget:
		outcome = "partial: UNFINISHED — the sub-agent reached its tool-call budget before it was done; calls in its final batch were not executed. What follows is real but partial; decide whether to spawn a continuation.\n\n" + body
	case result.Spoken == "":
		outcome = "unserved: " + body
	case identity.HasOutcomeVerdict(result.Spoken):
		outcome = result.Spoken
	default:
		outcome = "partial: ended without a stated verdict; its final message follows.\n\n" + result.Spoken
	}
	modelID := modelIDOf(result, target)
	tokens := fmt.Sprintf("%d", accTokens)
	if !result.Usage.Complete() {
		tokens = ">=" + tokens
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
	header := subagentOutcomeHeader(p.Role, modelID, target.fallback, target.cause,
		result.RoundsUsed, cfg.Agency.SubagentMaxToolRounds,
		result.ToolCallsUsed, cfg.Agency.SubagentMaxToolCalls,
		result.Usage.Calls, tokens, result.ExhaustedBudget || result.ExhaustedCallBudget)
	if leg > 1 {
		// .
		// .
		// .
		header = strings.TrimSuffix(header, "]") + fmt.Sprintf(" legs=%d/%d]", leg, maxLegs)
		if bounded {
			outcome += fmt.Sprintf("\nThis was leg %d of %d; the chain is exhausted.", leg, maxLegs)
		}
	}
	// .
	if i := strings.IndexByte(outcome, '\n'); i >= 0 {
		outcome = outcome[:i] + "\n" + header + outcome[i:]
	} else {
		outcome = outcome + "\n" + header
	}
	if err != nil || result.ExhaustedBudget || result.ExhaustedCallBudget {
		h.a.markFleetSpent()
	}
	if deliveryErr := h.a.store.DeliverWorkSession(p.SessionID, outcome, store.EvidenceWorkerReportOnly, ""); deliveryErr != nil {
		// .
		// .
		// .
		// .
		log.Printf("SUBAGENT %s: outcome could not be delivered (run err: %v): %v — item completed to prevent trajectory replay", p.SessionID, err, deliveryErr)
		return nil
	}
	if err != nil {
		log.Printf("SUBAGENT %s: run failed after execution began (delivered as FAILED, not retried): %v", p.SessionID, err)
	}
	h.closeChild(p, leg, accCalls, accTokens, accWall, modelID, err, outcome, compactText(outcome, 120))
	return nil
}

// .
// .
func subagentNotice(goal, outcome string) string {
	// .
	// .
	// .
	first := outcome
	if i := strings.IndexByte(first, '\n'); i >= 0 {
		first = first[:i]
	}
	status := "delivered"
	if i := strings.IndexByte(first, ':'); i > 0 {
		status = first[:i]
	}
	switch {
	case strings.Contains(first, "round ceiling"):
		status += " (rounds)"
	case strings.Contains(first, "tool-call budget"):
		status += " (calls)"
	case strings.Contains(first, "FAILED") || strings.Contains(first, "failed before start"):
		status += " (failed)"
	}
	return "[sub-agent done] " + compactText(strings.TrimSpace(goal), 120) + " — " + status
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

// .
// .
func modelIDOf(result conversation.Result, target runTarget) string {
	if result.ModelID != "" {
		return result.ModelID
	}
	return target.modelID
}

// .
// .
// .
// .
func continuationPreface(leg, maxLegs int, ws *store.WorkSession) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[continuation leg %d of %d] Your previous leg ended at its budget. This is the SAME work session; your record of it:", leg, maxLegs)
	if ws == nil {
		b.WriteString("\n(the session could not be read this leg)")
	} else {
		if ws.Focus != "" {
			b.WriteString("\nFocus: " + ws.Focus)
		}
		if ws.NextMove != "" {
			b.WriteString("\nNext move: " + ws.NextMove)
		}
		if ws.Plan != "" {
			b.WriteString("\nPlan:\n" + ws.Plan)
		}
		if ws.State != "" {
			b.WriteString("\nState: " + ws.State)
		}
	}
	if ws == nil || ws.NextMove == "" {
		b.WriteString("\nNo next move was recorded: `work update next_move=` with the exact resume point first, then continue.")
	} else {
		b.WriteString("\nContinue from the next move.")
	}
	b.WriteString(" Declare `steps=` for this leg on `work update` before your first act; at a checkpoint record `next_move=`, because your next leg starts from it.")
	return b.String()
}

func buildSubagentGoal(depth int, goal string, rounds, calls, legs, wallSeconds int) string {
	if legs < 1 {
		legs = 1
	}
	// .
	// .
	// .
	return fmt.Sprintf("[sub-agent, depth %d] Your sub-goal: %s\n\n"+
		"Work it to completion with your tools, staying strictly on this sub-goal — "+
		"do not expand scope. Finish with `work deliver result=` beginning served:, partial: or "+
		"unserved: — your verdict on this sub-goal — then what you established; that delivery ends "+
		"your run and is what your parent reads. A final message without it is delivered as it "+
		"stands. Make it usable by your synthesizing self:\n"+
		"- VERDICT first (one line: what you established)\n"+
		"- EVIDENCE second (file:line, command output, or citation for each claim)\n"+
		"- OPEN QUESTIONS last (what you could not resolve)\n\n"+
		"Your budget: %d rounds and %d calls per leg, up to %d leg(s), %d s wall per leg. "+
		"Reads first; before your first call that changes anything, `work update steps=` on your session "+
		"with the count the traced plan implies. At a checkpoint, `work update next_move=` with the exact "+
		"resume point — a leg that ends at its budget continues from it; a leg that ends without one starts by asking for it.",
		depth, goal, rounds, calls, legs, wallSeconds)
}

type runTarget struct {
	client   conversation.LLMClient
	budget   int
	modelID  string
	fallback bool
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	cause string
}

func (a *App) activeRunTarget(fallback bool, cause string) runTarget {
	a.cfgMu.RLock()
	defer a.cfgMu.RUnlock()
	client := a.llmSwap.Current()
	return runTarget{client: client, budget: a.composer.MaxTokens(), modelID: client.ModelName(), fallback: fallback, cause: cause}
}

// .
// .
// .
// .
// .
// .
// .
// .
func (a *App) routeIsLocal(role string) bool {
	cfg := a.configSnapshot()
	reg, err := a.loadProviders()
	if err != nil {
		return false
	}
	named := func(name string) bool {
		for _, e := range reg.Providers {
			if e.Name == name {
				return e.Local
			}
		}
		return false
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
	if role != "" {
		if route, ok := cfg.Agency.Roles[role]; ok {
			return named(route.Provider)
		}
		if cfg.Agency.PreferLocalForRoles {
			if _, ok := localProviderEntry(reg); ok {
				return true
			}
		}
	}
	return named(cfg.LLM.Provider)
}

func (a *App) resolveRunTarget(role string) runTarget {
	if role == "" {
		return a.activeRunTarget(false, "")
	}
	cfg := a.configSnapshot()
	reg, regErr := a.loadProviders()

	// .
	// .
	// .
	// .
	// .
	if route, ok := cfg.Agency.Roles[role]; ok {
		if regErr != nil {
			log.Printf("subagent role %q: providers unavailable (%v) — using the active model", role, regErr)
			return a.activeRunTarget(true, "providers-unavailable")
		}
		cc, entry, err := a.resolveLLMConfig(LLMConfig{Provider: route.Provider, Model: route.Model, APIKeyEnv: cfg.LLM.APIKeyEnv}, reg)
		if err != nil {
			log.Printf("subagent role %q: route %s/%s did not resolve (%v) — using the active model", role, route.Provider, route.Model, err)
			return a.activeRunTarget(true, "route-down")
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
	return a.activeRunTarget(true, "no-route")
}

// .
// .
// .
func localProviderEntry(reg *providerRegistry) (providerEntry, bool) {
	for _, e := range reg.Providers {
		if e.Local {
			return e, true
		}
	}
	return providerEntry{}, false
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
func subagentOutcomeHeader(role, modelID string, fallback bool, cause string,
	rounds, roundLimit, toolCalls, callLimit, llmCalls int, tokens string, exhausted bool) string {
	return fmt.Sprintf("[sub-agent role=%q model=%q fallback=%t cause=%q rounds=%d/%d tool_calls=%d/%d llm_calls=%d tokens=%s exhausted=%t]",
		role, modelID, fallback, cause, rounds, roundLimit, toolCalls, callLimit, llmCalls, tokens, exhausted)
}

// .
// .
// .
// .
// .
func (h *subagentHandler) closeChild(p identity.SubagentRequest, leg, calls, tokens int, wallMs int64, modelID string, runErr error, outcome, event string) {
	// .
	// .
	if merr := h.a.store.InsertSubagentMetric(store.SubagentMetric{
		SessionID: p.SessionID, TsMs: time.Now().UTC().UnixMilli(),
		Calls: calls, Tokens: tokens,
		WallMs: wallMs, Failed: runErr != nil,
		Role: p.Role, Model: modelID, Legs: leg,
	}); merr != nil {
		log.Printf("SUBAGENT %s: child metric not recorded: %v", p.SessionID, merr)
	}
	h.a.emitToolEvent("subagent", "done", fmt.Sprintf("%s: %s", p.SessionID, event))
	if h.a.dashboard != nil {
		h.a.dashboard.PokeOutbox()
		h.a.dashboard.BroadcastWork()
	}
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	if hw := h.a.configSnapshot().Agency.HarvestWake; hw == nil || *hw {
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
		gateCtx, cancelGate := context.WithTimeout(h.a.bgCtx, harvestGateWait)
		if !h.a.runBackground(func() {
			defer cancelGate()
			h.a.wakeSubagentDelivery(gateCtx, p.SessionID, p.Goal)
		}) {
			cancelGate()
		}
	}
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	if oerr := h.a.store.AddOutboxMessage("subagent_"+p.SessionID, "operator", "",
		subagentNotice(p.Goal, outcome), nil); oerr != nil {
		log.Printf("SUBAGENT %s: outbox notice failed: %v", p.SessionID, oerr)
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
}

// .
// .
// .
func (h *subagentHandler) deliveredResult(id string) (string, bool) {
	ws, err := h.a.store.WorkSessionByID(id)
	if err != nil || ws == nil || ws.Status != "delivered" || strings.HasPrefix(ws.Result, "unserved: failed before start:") {
		return "", false
	}
	return ws.Result, true
}

func (h *subagentHandler) sessionDelivered(id string) bool {
	_, ok := h.deliveredResult(id)
	return ok
}
