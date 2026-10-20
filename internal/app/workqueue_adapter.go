package app

import (
	"context"
	"errors"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
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

type alarmEnqueuerAdapter struct {
	ex *cognitive.Executor
}

const alarmLeaseMs = int64(30 * time.Minute / time.Millisecond)

func (a alarmEnqueuerAdapter) EnqueueAlarm(alarm store.Alarm) error {

	payload, _ := json.Marshal(map[string]any{
		"alarm_id":     alarm.AlarmID,
		"owner":        alarm.OwnerName,
		"clock":        alarm.Clock,
		"deadline":     alarm.Deadline,
		"repeat_every": alarm.RepeatEvery,
		"payload":      alarm.Payload,
	})

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

type alarmHandler struct {
	time ownerLookup
}

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

	return h.time.ApplyAlarmTransitions(alarm, result)
}

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

	ctx = logsink.WithWorker(ctx, "sa-"+shortSession(p.SessionID))

	limit := p.ParentBudget
	if limit <= 0 {

		limit = h.a.currentProvider().ThinkingBudget
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

	if h.sessionDelivered(p.SessionID) {

		if err := h.a.store.MarkUnmeasuredSubagentLegIncomplete(p.SessionID); err != nil {
			return fmt.Errorf("subagent %s: reconcile emission coverage: %w", p.SessionID, err)
		}
		logsink.InfoCtx(ctx, "subagent.refusal", "%s: leg %d not run — the session is already delivered", p.SessionID, leg)
		return nil
	}

	started, serr := h.a.store.WorkSessionStartedLeg(p.SessionID)
	if serr != nil {
		return fmt.Errorf("subagent %s: read its started leg: %w", p.SessionID, serr)
	}
	if started >= leg {
		return h.recoverStartedLeg(ctx, p, leg, maxLegs)
	}
	thinking := limit
	if p.ThinkingBudget > 0 && p.ThinkingBudget < limit {
		thinking = p.ThinkingBudget
	}

	wallSeconds := p.WallSeconds
	if wallSeconds <= 0 {
		wallSeconds = cfg.Agency.SubagentWallSeconds
	}
	wall := time.Duration(wallSeconds) * time.Second
	runCtx, cancel := context.WithTimeout(ctx, wall)
	defer cancel()

	runCtx = context.WithValue(runCtx, identity.SubagentDepth{}, p.Depth)
	runCtx = context.WithValue(runCtx, identity.SubagentBudget{}, thinking)
	runCtx = context.WithValue(runCtx, identity.SubagentWorkSession{}, p.SessionID)
	mintCount := 0
	runCtx = context.WithValue(runCtx, identity.SubagentMints{}, &mintCount)

	h.a.emitToolEvent("subagent", "spawn", fmt.Sprintf("depth %d: %s", p.Depth, p.Goal))

	goalText := buildSubagentGoal(p.Depth, p.Goal, cfg.Agency.SubagentMaxToolRounds, cfg.Agency.SubagentMaxToolCalls, maxLegs, wallSeconds)
	goalText += "\n\n" + sessionNotesLine(p.SessionID)
	if leg > 1 {
		ws, werr := h.a.store.WorkSessionByID(p.SessionID)
		if werr != nil {
			logsink.WarnCtx(ctx, "subagent.error", "%s: leg %d could not read its session: %v", p.SessionID, leg, werr)
		}
		goalText += "\n\n" + continuationPreface(leg, maxLegs, ws)
	}
	goalMsg := llm.Message{Role: "user", Content: goalText}
	target := h.a.resolveRunTarget(p.Role)

	h.a.emitPluginEvent(pluginhost.TopicSubagentSpawned, map[string]any{"session": p.SessionID, "role": p.Role, "model": target.modelID, "depth": p.Depth, "leg": leg})
	reserve, err := h.a.promptReserve(goalMsg, 0)
	if err != nil {
		return h.failBeforeStart(ctx, w, p, leg, maxLegs, "request estimate", err)
	}
	contextSections := h.a.pluginContext(runCtx, p.Goal)
	prompt, err := h.a.composer.ComposeChildWithContext(target.budget, reserve, p.Context != "full", contextSections)
	if err != nil {
		return h.failBeforeStart(ctx, w, p, leg, maxLegs, "compose", err)
	}
	recordPluginContextComposition(runCtx, prompt, contextSections)
	system := h.a.gatedSystem(prompt)

	var predictedNow func() (remaining, ordinal int)
	if legsOn {
		meter, release := h.a.registerLegMeter(p.SessionID)
		defer release()
		predictedNow = meter.get
	}

	runStarted := false
	subLoop := conversation.New(target.client, appToolExecutor{h.a}, appToolDefiner{h.a},
		appTranscript{st: h.a.store, actor: p.SessionID, safe: h.a.safeTools}, appEmitter{a: h.a, actor: p.SessionID}, conversation.Config{

			MaxIterations: cfg.Agency.SubagentMaxToolRounds,

			MaxToolCalls: cfg.Agency.SubagentMaxToolCalls,

			TurnTokenBudget:    cfg.Agency.TurnTokenBudget,
			ToolSchemaRefusals: cfg.Agency.ToolSchemaRefusals,
			Checkpoint:         cfg.Agency.checkpoint(),

			MaxToolResultChars:    h.a.engine.ToolResultLimit(),
			ContextBudgetTokens:   target.budget,
			ContextBudgetFallback: target.budgetGuess,
			ThinkingBudget:        thinking,
			PredictedThisTurn:     predictedNow,

			ReplaySafe: replaySafeHook(h.a.toolReg),

			ImagesRefused: h.a.imagesRefused,

			BeforeFirstCall: func() error {
				if err := h.a.store.StartWorkSessionLeg(p.SessionID, leg); err != nil {
					return err
				}
				runStarted = true
				return nil
			},
		})

	if target.provider != "" {
		runCtx = withTurnImages(runCtx, target.provider, target.modelID)
	} else {
		runCtx = withTurnImages(runCtx, "", "")
	}

	if h.a.dashboard != nil {
		h.a.dashboard.BroadcastWork()
	}
	runStart := time.Now()
	result, err := subLoop.RunSystem(runCtx, system, []llm.Message{goalMsg}, 0)
	if err != nil && !runStarted {
		return h.failBeforeStart(ctx, w, p, leg, maxLegs, "first call", err)
	}
	runWall := time.Since(runStart)
	accCalls := p.AccCalls + result.ToolCallsUsed
	accTokens := p.AccTokens + result.Usage.TotalTokens
	accWall := p.AccWallMs + runWall.Milliseconds()
	p.AccEmissions = accumulateEmissions(p.AccEmissions, leg, result)

	if delivered, ok := h.deliveredResult(p.SessionID); ok {
		logsink.InfoCtx(ctx, "subagent.end", "%s: leg %d delivered by the child itself after %d calls — chain closed", p.SessionID, leg, result.ToolCallsUsed)
		if err != nil {
			logsink.WarnCtx(ctx, "subagent.error", "%s: run ended in error after its own delivery (the delivered result stands): %v", p.SessionID, err)
		}
		h.closeChild(p, leg, accCalls, accTokens, accWall, modelIDOf(result, target), err, delivered, fmt.Sprintf("delivered by the child (leg %d)", leg))
		return nil
	}

	unfinished := result.ExhaustedBudget || result.ExhaustedCallBudget || capReasonOf(result) != noCap
	bounded := err == nil && unfinished
	if bounded && legsOn && leg < maxLegs {
		if merr := h.a.store.InsertSubagentMetric(store.SubagentMetric{
			SessionID: p.SessionID, TsMs: time.Now().UTC().UnixMilli(),
			Calls: accCalls, Tokens: accTokens, WallMs: accWall, Failed: false,
			Role: p.Role, Model: modelIDOf(result, target), Legs: leg,
			Emissions: p.AccEmissions,
		}); merr != nil {
			logsink.WarnCtx(ctx, "subagent.error", "%s: leg %d metric not recorded: %v", p.SessionID, leg, merr)
		}
		next := p
		next.Leg, next.AccCalls, next.AccTokens, next.AccWallMs = leg+1, accCalls, accTokens, accWall
		next.WallSeconds = wallSeconds
		payload, perr := json.Marshal(next)
		if perr != nil {
			logsink.WarnCtx(ctx, "subagent.error", "%s: leg %d could not encode its continuation: %v — delivering unfinished", p.SessionID, leg, perr)
		} else if _, qerr := h.a.store.EnqueueWork(&store.WorkItem{
			Kind: identity.SubagentWorkKind, Payload: string(payload),
			DedupKey: fmt.Sprintf("%s#%d", p.SessionID, leg+1), Source: "identity",
			LeaseMs: int64(wallSeconds+60) * 1000,
		}); qerr != nil {
			logsink.WarnCtx(ctx, "subagent.error", "%s: leg %d could not enqueue leg %d: %v — delivering unfinished", p.SessionID, leg, leg+1, qerr)
		} else {
			ended := legEnding(result)
			logsink.InfoCtx(ctx, "subagent.end", "%s: leg %d of %d ended %s after %d calls — leg %d enqueued", p.SessionID, leg, maxLegs, ended, result.ToolCallsUsed, leg+1)
			h.a.emitToolEvent("subagent", "continue", fmt.Sprintf("%s: leg %d → %d (%s)", p.SessionID, leg, leg+1, ended))
			if h.a.dashboard != nil {
				h.a.dashboard.BroadcastWork()
			}
			if h.a.queueWake != nil {
				h.a.queueWake()
			}
			return nil
		}
	}

	body := result.Spoken
	if body == "" {
		body = "(completed with no spoken outcome)"
	}
	outcome := ""
	switch {
	case err != nil:

		outcome = "unserved: FAILED: " + err.Error()
	case result.ExhaustedBudget:
		outcome = "partial: UNFINISHED — the sub-agent reached its round ceiling before it was done. What follows is real but partial; decide whether to spawn a continuation.\n\n" + body
	case result.ExhaustedCallBudget:
		outcome = "partial: UNFINISHED — the sub-agent reached its tool-call budget before it was done; calls in its final batch were not executed. What follows is real but partial; decide whether to spawn a continuation.\n\n" + body
	case capReasonOf(result) != noCap:

		why := fmt.Sprintf("this was leg %d of %d, the last", leg, maxLegs)
		if !legsOn {
			why = "sub-agent continuation is off"
		} else if leg < maxLegs {
			why = "its next leg could not be enqueued"
		}
		outcome = "partial: UNFINISHED — the sub-agent stopped " + capReasonOf(result).how() + " before it was done, and nothing continues it: " + why + ". What follows is real but partial; decide whether to spawn a continuation.\n\n" + body
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

	header := subagentOutcomeHeader(p.Role, modelID, target.cause,
		result.RoundsUsed, cfg.Agency.SubagentMaxToolRounds,
		result.ToolCallsUsed, cfg.Agency.SubagentMaxToolCalls,
		result.Usage.Calls, tokens, unfinished)
	if leg > 1 {

		header = strings.TrimSuffix(header, "]") + fmt.Sprintf(" legs=%d/%d]", leg, maxLegs)
		if bounded && capReasonOf(result) == noCap {
			outcome += fmt.Sprintf("\nThis was leg %d of %d; the chain is exhausted.", leg, maxLegs)
		}
	}

	if i := strings.IndexByte(outcome, '\n'); i >= 0 {
		outcome = outcome[:i] + "\n" + header + outcome[i:]
	} else {
		outcome = outcome + "\n" + header
	}
	if err != nil || unfinished {
		h.a.markFleetSpent()
	}

	if deliveryErr := h.a.store.DeliverWorkSession(context.WithoutCancel(ctx), p.SessionID, outcome, store.EvidenceWorkerReportOnly, ""); deliveryErr != nil {

		logsink.ErrorCtx(ctx, "subagent.error", "%s: outcome could not be delivered (run err: %v): %v — item completed to prevent trajectory replay", p.SessionID, err, deliveryErr)
		return nil
	}
	if err != nil {
		logsink.WarnCtx(ctx, "subagent.error", "%s: run failed after execution began (delivered as FAILED, not retried): %v", p.SessionID, err)
	}
	h.closeChild(p, leg, accCalls, accTokens, accWall, modelID, err, outcome, compactText(outcome, 120))
	return nil
}

var errLegInterrupted = errors.New("the leg's run was interrupted by a runtime restart")

func (h *subagentHandler) recoverStartedLeg(ctx context.Context, p identity.SubagentRequest, leg, maxLegs int) error {
	handed, err := h.a.store.HasWorkItem(identity.SubagentWorkKind, fmt.Sprintf("%s#%d", p.SessionID, leg+1))
	if err != nil {
		return fmt.Errorf("subagent %s: look for leg %d: %w", p.SessionID, leg+1, err)
	}
	if handed {
		logsink.InfoCtx(ctx, "subagent.end", "%s: leg %d came back after it had handed over to leg %d — nothing owed", p.SessionID, leg, leg+1)
		return nil
	}
	ws, werr := h.a.store.WorkSessionByID(p.SessionID)
	if werr != nil {
		logsink.WarnCtx(ctx, "subagent.error", "%s: the interrupted leg %d could not read its session: %v", p.SessionID, leg, werr)
	}
	record := sessionRecord(ws)
	if record == "" {
		record = "\n(the session recorded nothing of its progress)"
	}
	outcome := store.InterruptedLeg(fmt.Sprintf(" — leg %d of %d had begun, and it is not run again: its calls may already have acted. What the session recorded:", leg, maxLegs) + record)
	logsink.WarnCtx(ctx, "subagent.error", "%s: leg %d had begun when the runtime stopped — delivered as external_effect_unknown, not run again", p.SessionID, leg)
	if deliveryErr := h.a.store.DeliverWorkSession(context.WithoutCancel(ctx), p.SessionID, outcome, store.EvidenceExternalUnknown, ""); deliveryErr != nil {
		logsink.ErrorCtx(ctx, "subagent.error", "%s: the interrupted leg %d could not be delivered: %v — item completed to prevent trajectory replay", p.SessionID, leg, deliveryErr)
		return nil
	}
	h.a.markFleetSpent()

	p.AccEmissions = accumulateEmissions(p.AccEmissions, leg, conversation.Result{})
	h.closeChild(p, leg, p.AccCalls, p.AccTokens, p.AccWallMs, "", errLegInterrupted, outcome, compactText(outcome, 120))
	return nil
}

func (h *subagentHandler) failBeforeStart(ctx context.Context, w *store.WorkItem, p identity.SubagentRequest, leg, maxLegs int, phase string, cause error) error {
	err := fmt.Errorf("subagent %s: %w", phase, cause)
	last := w.RetryCount+1 >= w.MaxRetries
	outcome, evidence := store.PreStartFailure(cause.Error()), store.EvidenceNotRun
	if leg > 1 {
		if !last {
			return err
		}
		outcome = fmt.Sprintf("partial: UNFINISHED — leg %d of %d could not start (%s), and nothing continues it. The legs before it ran, and their calls may already have acted.", leg, maxLegs, cause.Error())
		evidence = store.EvidencePartialOrMixed
	}
	if deliveryErr := h.a.store.DeliverWorkSession(context.WithoutCancel(ctx), p.SessionID, outcome, evidence, ""); deliveryErr != nil {
		return errors.Join(err, fmt.Errorf("record subagent start failure: %w", deliveryErr))
	}
	if last {
		logsink.WarnCtx(ctx, "subagent.error", "%s: leg %d failed before start on its last attempt (%d of %d) — delivered as it stands and closed, not retried", p.SessionID, leg, w.RetryCount+1, w.MaxRetries)
		h.a.markFleetSpent()
		h.closeChild(p, leg, p.AccCalls, p.AccTokens, p.AccWallMs, "", err, outcome, compactText(outcome, 120))
	}
	return err
}

func subagentNotice(goal, outcome string) string {

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
	case strings.Contains(first, capTokenFence.how()):
		status += " (tokens)"
	case strings.Contains(first, capContext.how()):
		status += " (context)"
	case strings.Contains(first, capDeclared.how()):
		status += " (declared budget)"
	case strings.Contains(first, "FAILED") || store.IsPreStartFailure(first):
		status += " (failed)"
	}
	return "[sub-agent done] " + compactText(strings.TrimSpace(goal), noticeNameChars) + " — " + status
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

func legEnding(r conversation.Result) string {
	switch {
	case r.ExhaustedCallBudget:
		return "at its call ceiling"
	case r.ExhaustedBudget:
		return "at its round ceiling"
	}
	return capReasonOf(r).how()
}

func modelIDOf(result conversation.Result, target runTarget) string {
	if result.ModelID != "" {
		return result.ModelID
	}
	return target.modelID
}

func continuationPreface(leg, maxLegs int, ws *store.WorkSession) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[continuation leg %d of %d] Your previous leg ended at its budget. This is the SAME work session; your record of it:", leg, maxLegs)
	if ws == nil {
		b.WriteString("\n(the session could not be read this leg)")
	} else {
		b.WriteString(sessionRecord(ws))
	}
	if ws == nil || ws.NextMove == "" {
		b.WriteString("\nNo next move was recorded: `work update next_move=` with the exact resume point first, then continue.")
	} else {
		b.WriteString("\nContinue from the next move.")
	}
	b.WriteString(" Declare `steps=` for this leg on `work update` before your first act; at a checkpoint record `next_move=`, because your next leg starts from it.")
	return b.String()
}

func sessionRecord(ws *store.WorkSession) string {
	if ws == nil {
		return ""
	}
	if card := resumeCardFor(ws); card != "" {
		return "\n" + card
	}
	return ""
}

func buildSubagentGoal(depth int, goal string, rounds, calls, legs, wallSeconds int) string {
	if legs < 1 {
		legs = 1
	}

	return fmt.Sprintf("[sub-agent, depth %d] Your sub-goal: %s\n\n"+
		"Work it to completion with your tools, staying strictly on this sub-goal — "+
		"do not expand scope. Finish with `work deliver result=` beginning served:, partial: or "+
		"unserved: — your verdict on this sub-goal — then what you established; that delivery ends "+
		"your run and is what your parent reads. Omit evidence to use worker_report_only; "+
		"locally_verified and host_receipted are unavailable from this worker session. "+
		"A final message without it is delivered as it "+
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
	client  conversation.LLMClient
	budget  int
	modelID string

	provider string

	budgetGuess bool

	cause string
}

func (t runTarget) fellBack() bool { return t.cause != "" }

func (a *App) activeRunTarget(cause string) runTarget {
	a.cfgMu.RLock()
	defer a.cfgMu.RUnlock()
	client := a.llmSwap.Current()
	_, src := a.currentPromptBudget()
	return runTarget{client: client, budget: a.composer.MaxTokens(), modelID: client.ModelName(), budgetGuess: src == budgetFallback, cause: cause}
}

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

	if role != "" {
		if route, ok := cfg.Agency.Roles[role]; ok {
			return named(route.Provider)
		}
		if cfg.Agency.PreferLocalForRoles && len(localEntries(reg)) > 0 {
			return true
		}
	}
	return named(cfg.LLM.Provider)
}

func (a *App) resolveRunTarget(role string) runTarget {
	if role == "" {
		return a.activeRunTarget("")
	}
	cfg := a.configSnapshot()
	reg, regErr := a.loadProviders()

	if route, ok := cfg.Agency.Roles[role]; ok {
		if regErr != nil {
			logsink.Warn("subagent.decision", "role %q: providers unavailable (%v) — using the active model", role, regErr)
			return a.activeRunTarget("providers-unavailable")
		}
		cc, entry, err := a.resolveRouteLLM(cfg.LLM, reg, route.Provider, route.Model)
		if err != nil {
			logsink.Warn("subagent.decision", "role %q: route %s/%s did not resolve (%v) — using the active model", role, route.Provider, route.Model, err)
			return a.activeRunTarget("route-down")
		}
		budget, src := promptBudgetFor(entry, cfg.Prompt.MaxTokens)
		logsink.Info("subagent.decision", "role %q routed: provider %q model %q (prompt budget %d)", role, entry.Name, cc.Model, budget)
		return runTarget{client: a.newLLMClient(cc, budget), budget: budget, modelID: cc.Model, provider: entry.Name, budgetGuess: src == budgetFallback}
	}

	if cfg.Agency.PreferLocalForRoles && regErr == nil {
		if local, ok := a.firstHealthyLocal(reg); ok {
			cc, entry, err := a.resolveRouteLLM(cfg.LLM, reg, local.Name, "")
			if err == nil {
				budget, src := promptBudgetFor(entry, cfg.Prompt.MaxTokens)
				logsink.Info("subagent.decision", "role %q → local model %q on %q (checkbox; prompt budget %d)", role, cc.Model, entry.Name, budget)
				return runTarget{client: a.newLLMClient(cc, budget), budget: budget, modelID: cc.Model, provider: entry.Name, budgetGuess: src == budgetFallback}
			}
			logsink.Warn("subagent.decision", "role %q: local entry %q did not resolve (%v) — using the active model", role, local.Name, err)
		}
	}

	logsink.Info("subagent.decision", "role %q: no route — using the active model", role)
	return a.activeRunTarget("no-route")
}

func localEntries(reg *providerRegistry) []providerEntry {
	var marked []providerEntry
	for _, e := range reg.Providers {
		if e.Local {
			marked = append(marked, e)
		}
	}
	return marked
}

func (a *App) firstHealthyLocal(reg *providerRegistry) (providerEntry, bool) {
	marked := localEntries(reg)
	if len(marked) == 0 {
		return providerEntry{}, false
	}
	probes := a.probeProviders(&providerRegistry{Providers: marked})
	for _, e := range marked {
		if probes[e.Name].state == probeOK {
			return e, true
		}
	}
	return providerEntry{}, false
}

func subagentOutcomeHeader(role, modelID, cause string,
	rounds, roundLimit, toolCalls, callLimit, llmCalls int, tokens string, exhausted bool) string {
	return fmt.Sprintf("[sub-agent role=%q model=%q fallback=%t cause=%q rounds=%d/%d tool_calls=%d/%d llm_calls=%d tokens=%s exhausted=%t]",
		role, modelID, runTarget{cause: cause}.fellBack(), cause, rounds, roundLimit, toolCalls, callLimit, llmCalls, tokens, exhausted)
}

func (h *subagentHandler) closeChild(p identity.SubagentRequest, leg, calls, tokens int, wallMs int64, modelID string, runErr error, outcome, event string) {

	if merr := h.a.store.InsertSubagentMetric(store.SubagentMetric{
		SessionID: p.SessionID, TsMs: time.Now().UTC().UnixMilli(),
		Calls: calls, Tokens: tokens,
		WallMs: wallMs, Failed: runErr != nil,
		Role: p.Role, Model: modelID, Legs: leg,
		Emissions: p.AccEmissions,
	}); merr != nil {
		logsink.Warn("subagent.error", "%s: child metric not recorded: %v", p.SessionID, merr)
	}
	h.a.emitToolEvent("subagent", "done", fmt.Sprintf("%s: %s", p.SessionID, event))
	if h.a.dashboard != nil {
		h.a.dashboard.PokeOutbox()
		h.a.dashboard.BroadcastWork()
	}

	if hw := h.a.configSnapshot().Agency.HarvestWake; hw == nil || *hw {

		gateCtx, cancelGate := context.WithTimeout(h.a.bgCtx, harvestGateWait)
		if !h.a.runBackground(func() {
			defer cancelGate()
			h.a.wakeSubagentDelivery(gateCtx, p.SessionID, p.Goal)
		}) {
			cancelGate()
		}
	}

	if oerr := h.a.store.AddOutboxNotice("subagent_"+p.SessionID, "operator", "",
		subagentNotice(p.Goal, outcome), nil); oerr != nil {
		logsink.Warn("subagent.error", "%s: outbox notice failed: %v", p.SessionID, oerr)
	}

}

func accumulateEmissions(prior *store.ToolCallEmissions, leg int, result conversation.Result) *store.ToolCallEmissions {
	out := &store.ToolCallEmissions{Count: result.ToolCallsEmitted, Complete: result.ToolCallCoverageComplete}
	if prior != nil {
		out.Count += prior.Count
		out.Complete = out.Complete && prior.Complete
	} else if leg > 1 {
		out.Complete = false
	}
	return out
}

func (h *subagentHandler) deliveredResult(id string) (string, bool) {
	ws, err := h.a.store.WorkSessionByID(id)
	if err != nil || ws == nil || ws.Status != "delivered" || store.IsPreStartFailure(ws.Result) {
		return "", false
	}
	return ws.Result, true
}

func (h *subagentHandler) sessionDelivered(id string) bool {
	_, ok := h.deliveredResult(id)
	return ok
}

func shortSession(id string) string {
	if len(id) > 6 {
		return id[:6]
	}
	return id
}
