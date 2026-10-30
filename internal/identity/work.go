package identity

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"
	"uuid"

	"github.com/aiii-dot-id/aii-os/internal/store"
)

const SubagentWorkKind = store.SubagentWorkKind

type SubagentRequest struct {
	Goal      string `json:"goal"`
	Depth     int    `json:"depth"`
	SessionID string `json:"ws_id"`

	ParentSessionID string `json:"parent_work_session_id,omitempty"`
	ThinkingBudget  int    `json:"thinking_budget"`
	Context         string `json:"context"`
	ParentBudget    int    `json:"parent_budget"`
	Role            string `json:"role"`

	WallSeconds int `json:"wall_seconds,omitempty"`

	Leg       int   `json:"leg,omitempty"`
	AccCalls  int   `json:"acc_calls,omitempty"`
	AccTokens int   `json:"acc_tokens,omitempty"`
	AccWallMs int64 `json:"acc_wall_ms,omitempty"`

	AccEmissions *store.ToolCallEmissions `json:"acc_emissions,omitempty"`
}

func (r SubagentRequest) Validate() error {
	switch {
	case r.Goal == "":
		return fmt.Errorf("goal is required")
	case r.SessionID == "":
		return fmt.Errorf("work session is required")
	case r.Depth < 1:
		return fmt.Errorf("depth must be positive")
	case r.Context != "folded" && r.Context != "full":
		return fmt.Errorf("context must be folded or full")
	default:
		return nil
	}
}

type workStore interface {
	EnqueueWorkOfKind(item *store.WorkItem, limit int, sessionID, description, family string) (int, bool, error)
	LiveSubagentSessions() ([]store.WorkSession, error)
	UnharvestedDeliveries(limit int) ([]store.WorkSession, error)
	WorkSessionByID(id string) (*store.WorkSession, error)
	StartWorkSessionWith(ctx context.Context, id, description string, w store.WorkStart) error
	SetWorkSessionFamily(ctx context.Context, id, family string) error
	SetWorkCheck(ctx context.Context, id, command string) error
	SetStandingState(text string) error
	UpdateWorkState(ctx context.Context, sessionID, state string) error
	UpdateWorkPlan(ctx context.Context, sessionID string, focus, nextMove, plan, expectedEvidence, falsifier, decisionNeeded *string) error
	DeliverWorkSession(ctx context.Context, id, result, evidence, readback string) error
	ActiveWorkSession() (*store.WorkSession, error)
}

const workCheckParam = "(update) a command that exits 0 only if this work holds, written before the work; at deliver the host runs it once under the shell's rules and keeps its exit status beside your result"

const workFamilyParam = "(start, update, spawn) the kind of work as a short slug, such as deploy-preview; reuse a kind you have done (start lists them) so attempts compare; a spawn takes yours unless given; (measure) that kind's attempts, outcomes and shortest served path"

const startFieldsWords = "rejected_before_effect (start.fields): no effect — start takes description, family and update's fields, not %s; no session was started"

const (
	startOlderActive = "\nSession %s (%s) is still active; calls are counted to this new one until it is delivered."
	startOlderUnread = "work start: the active session could not be read: %w; no session was started"
)

var startReads = map[string]bool{"action": true, "description": true, "family": true, "state": true, "focus": true, "next_move": true,
	"plan": true, "expected_evidence": true, "falsifier": true, "decision_needed": true, "choices": true, "connector": true,
	"standing": true, "steps": true, "independent": true, "check": true}

func startExtras(args map[string]any) []string {
	var extra []string
	for k, v := range args {
		if startReads[k] {
			continue
		}
		if strings.HasPrefix(k, "_") || isEmptyArg(v) {
			continue
		}
		extra = append(extra, k)
	}
	slices.Sort(extra)
	return extra
}

func startGiven(args map[string]any) map[string]any {
	given := make(map[string]any, len(args))
	for k, v := range args {
		if !isEmptyArg(v) {
			given[k] = v
		}
	}
	return given
}

type StartedSession struct{}

const startedButWords = "Started %s, but its decision was stored and could not be put to your operator: %v. Resume this session; do not start another"

func isEmptyArg(v any) bool {
	switch v := v.(type) {
	case nil:
		return true
	case string:
		return v == ""
	case bool:
		return !v
	case float64:
		return v == 0
	case int:
		return v == 0
	case []any:
		return len(v) == 0
	case map[string]any:
		return len(v) == 0
	}
	return false
}

func workFields(args map[string]any) store.WorkStart {
	var f store.WorkStart
	for key, into := range map[string]**string{"state": &f.State, "focus": &f.Focus, "next_move": &f.NextMove, "plan": &f.Plan,
		"expected_evidence": &f.ExpectedEvidence, "falsifier": &f.Falsifier, "decision_needed": &f.DecisionNeeded} {
		if v, ok := args[key].(string); ok {
			*into = &v
		}
	}
	return f
}

func (e *Engine) putDecision(wsID string, decisionNeeded *string, args map[string]any) error {
	if decisionNeeded == nil || e.askProposer == nil {
		return nil
	}
	choices, connector := decisionCard(args)
	if err := e.askProposer(wsID, *decisionNeeded, choices, connector); err != nil {
		return fmt.Errorf("the decision was stored but could not be put to your operator: %w", err)
	}
	return nil
}

func decisionCard(args map[string]any) (choices []string, connector string) {
	if raw, ok := args["choices"].([]any); ok {
		for _, c := range raw {
			if s, ok := c.(string); ok && strings.TrimSpace(s) != "" {
				choices = append(choices, strings.TrimSpace(s))
			}
		}
	}
	connector, _ = args["connector"].(string)
	return choices, strings.TrimSpace(connector)
}

const decisionUnfit = "rejected_before_effect (work.decision): no effect — your operator's card cannot carry this decision: %v; nothing was written"

func (e *Engine) decisionFits(session string, args map[string]any) error {
	text, ok := args["decision_needed"].(string)
	if !ok || e.askFits == nil {
		return nil
	}
	choices, connector := decisionCard(args)
	if err := e.askFits(session, text, choices, connector); err != nil {
		return withCode("work.decision", fmt.Errorf(decisionUnfit, err))
	}
	return nil
}

func (e *Engine) verbWork(ctx context.Context, args map[string]any) (string, error) {
	action, _ := args["action"].(string)
	if action == "" {
		action = "start"
	}
	var startedID string

	if out, handled, err := e.workAbsorbed(ctx, action, args); handled {
		return out, err
	}

	switch action {
	case "spawn":

		goal, _ := args["goal"].(string)
		if goal == "" {
			goal, _ = args["_positional"].(string)
		}
		if goal == "" {
			return "", withCode("spawn.goal", fmt.Errorf("spawn requires a goal"))
		}
		family, _ := args["family"].(string)
		family = strings.TrimSpace(family)
		if family != "" && !store.FamilySlug(family) {
			return "", rejectf("spawn.family", "no effect — family %q is not a slug: lowercase letters, digits and hyphens, at most 40 characters; nothing was spawned", family)
		}
		depth := 0
		if d, ok := args["_subagent_depth"].(int); ok {
			depth = d
		}

		maxDepth, maxParallel, _, _ := e.agencyLimits()
		queueOn := e.spawnQueueOn()
		bound := maxParallel
		if queueOn {
			bound = maxParallel * 2
		}
		if maxDepth <= 0 {
			return "", withCode("spawn.disabled", fmt.Errorf("spawn refused: sub-agents are disabled (agency.max_subagent_depth = 0) — ask your operator to enable them"))
		}
		if depth+1 > maxDepth {
			return "", withCode("spawn.depth", fmt.Errorf("spawn refused: depth %d would exceed agency.max_subagent_depth %d — finish this level's goal or ask your operator to raise the ceiling", depth+1, maxDepth))
		}
		parentSession, _ := ctx.Value(SubagentWorkSession{}).(string)
		if parentSession == "" {
			parent, err := e.store.ActiveWorkSession()
			if err != nil {
				return "", fmt.Errorf("spawn parent: %w", err)
			}
			if parent != nil {
				parentSession = parent.ID
			}
		}

		wsID := "ws_" + uuid.New().String()

		thinking := 0
		if tb, ok := numArg(args["thinking_budget"]); ok {
			thinking = int(tb)
		}
		parentBudget := 0
		if pb, ok := args["_subagent_budget"].(int); ok {
			parentBudget = pb
		}
		ctxScope, _ := args["context"].(string)
		if ctxScope != "full" {
			ctxScope = "folded"
		}
		role, _ := args["role"].(string)

		spawn := e.spawnLimits(role)
		wallSeconds := spawn.WallSeconds
		request := SubagentRequest{
			Goal: goal, Depth: depth + 1, SessionID: wsID, ParentSessionID: parentSession,
			ThinkingBudget: thinking, Context: ctxScope,
			ParentBudget: parentBudget, Role: role,
			WallSeconds: wallSeconds,
		}
		payload, err := json.Marshal(request)
		if err != nil {
			return "", fmt.Errorf("encode subagent request: %w", err)
		}

		leaseMs := int64(wallSeconds+60) * 1000

		live, enqueued, err := e.store.EnqueueWorkOfKind(&store.WorkItem{
			Kind: SubagentWorkKind, Payload: string(payload), DedupKey: wsID, Source: "identity",
			LeaseMs: leaseMs,
		}, bound, wsID, store.SubagentDescription(goal), family)
		if err != nil {
			return "", fmt.Errorf("spawn enqueue: %w", err)
		}
		if !enqueued {
			if queueOn {
				return "", withCode("spawn.full", fmt.Errorf("spawn refused: %d sub-agents queued or running — the queue holds at most %d, twice agency.max_parallel_subagents (%d); harvest what is delivered or wait for a slot", live, bound, maxParallel))
			}
			return "", withCode("spawn.full", fmt.Errorf("spawn refused: %d sub-agents already live — agency.max_parallel_subagents is %d; wait for one to finish or ask your operator to raise it", live, maxParallel))
		}
		if e.workWake != nil {
			e.workWake()
		}
		if queueOn && live >= maxParallel {

			return fmt.Sprintf("Queued sub-agent %s (depth %d): %s — %d ahead of it for %d slot(s) (agency.max_parallel_subagents); it starts when a slot frees. Its outcome returns to your working state (Ring 4) and wakes you to harvest it; record this ID in your plan if it belongs to one. Do not spawn it again; keep doing independent work.", wsID, depth+1, goal, live, maxParallel) + spawnBudgetLine(spawn), nil
		}
		return fmt.Sprintf("Spawned sub-agent %s (depth %d): %s — its outcome returns to your working state (Ring 4) and wakes you to harvest it; record this ID in your plan if it belongs to one. Note what deserves to become memory.", wsID, depth+1, goal) + spawnBudgetLine(spawn), nil

	case "yield":

		answer, _ := args["answer"].(string)
		answer = strings.TrimSpace(answer)
		if e.yieldGate != nil && answer == "" {
			if need, why := e.yieldGate(); need {
				return "", fmt.Errorf("yield refused: %s. State the best current answer for the operator — answer= (what is established, what is missing, what would change it) — then yield", why)
			}
		}
		if answer != "" {
			return "Turn yielding with your answer carried to the operator — your gate frees when this round ends. Your session stays active; a delivery or message wakes you.", nil
		}
		return "Turn yielding — your gate frees when this round ends. Your session stays active; a delivery or message wakes you.", nil

	case "status":

		if id, _ := args["id"].(string); strings.TrimSpace(id) != "" {
			return e.workResult(ctx, strings.TrimSpace(id))
		}
		return e.workStatus(ctx)

	case "start":

		if extra := startExtras(args); len(extra) > 0 {
			return "", withCode("start.fields", fmt.Errorf(startFieldsWords, strings.Join(extra, ", ")))
		}
		desc, _ := args["description"].(string)
		if desc == "" {
			desc, _ = args["_positional"].(string)
		}
		family, _ := args["family"].(string)
		family = strings.TrimSpace(family)
		if family != "" && !store.FamilySlug(family) {
			return "", rejectf("start.family", "no effect — family %q is not a slug: lowercase letters, digits and hyphens, at most 40 characters (e.g. \"deploy-preview\")", family)
		}

		args = startGiven(args)
		if err := e.updateFits(args); err != nil {
			return "", err
		}
		if err := e.decisionFits("", args); err != nil {
			return "", err
		}
		with := workFields(args)
		if family != "" {
			with.Family = &family
		}
		if v, ok := args["standing"].(string); ok {
			with.Standing = &v
		}
		if v, ok := args["check"].(string); ok {
			with.Check = &v
		}
		wsID := "ws_" + uuid.New().String()

		older, err := e.store.ActiveWorkSession()
		if err != nil {
			return "", fmt.Errorf(startOlderUnread, err)
		}
		if slot, ok := ctx.Value(StartedSession{}).(*string); ok {
			defer func() { *slot = startedID }()
		}

		if err := e.store.StartWorkSessionWith(ctx, wsID, desc, with); err != nil {
			return "", err
		}
		startedID = wsID
		out := fmt.Sprintf("Started work session %s: %s", wsID, desc)
		if family != "" {
			out = fmt.Sprintf("Started work session %s (family %s): %s", wsID, family, desc)
		}
		if with.DecisionNeeded != nil && e.askProposer != nil {
			choices, connector := decisionCard(args)
			if err := e.askProposer(wsID, *with.DecisionNeeded, choices, connector); err != nil {

				return "", fmt.Errorf(startedButWords, wsID, err)
			}
		}
		if older != nil {
			out += fmt.Sprintf(startOlderActive, older.ID, recallExcerpt(older.Description, 120))
		}
		if e.workStarted != nil {
			out += e.workStarted(ctx, wsID, family, desc)
		}
		return out, nil

	case "update":

		if err := e.updateFits(args); err != nil {
			return "", err
		}
		if _, ok := args["decision_needed"]; ok {
			session, _ := e.currentWorkSessionID(ctx)
			if err := e.decisionFits(session, args); err != nil {
				return "", err
			}
		}
		standingSet := false
		if v, ok := args["standing"].(string); ok {
			if err := e.store.SetStandingState(v); err != nil {
				return "", fmt.Errorf("standing state not recorded: %w", err)
			}
			standingSet = true
		}

		f := workFields(args)
		if f.State == nil {
			if v, ok := args["_positional"].(string); ok {
				f.State = &v
			}
		}
		state, focus, nextMove, plan, expectedEvidence, falsifier, decisionNeeded := f.State, f.Focus, f.NextMove, f.Plan, f.ExpectedEvidence, f.Falsifier, f.DecisionNeeded
		var family *string
		if v, ok := args["family"].(string); ok {
			v = strings.TrimSpace(v)
			if !store.FamilySlug(v) {
				return "", rejectf("update.family", "no effect — family %q is not a slug: lowercase letters, digits and hyphens, at most 40 characters", v)
			}
			family = &v
		}
		check, hasCheck := args["check"].(string)
		sessionWork := state != nil || focus != nil || nextMove != nil || plan != nil || expectedEvidence != nil || falsifier != nil || decisionNeeded != nil || family != nil || hasCheck

		for _, k := range []string{"steps", "independent"} {
			if _, ok := args[k]; ok {
				sessionWork = true
			}
		}

		wsID, err := e.currentWorkSessionID(ctx)
		if err != nil {

			if standingSet && !sessionWork {
				return "Standing state updated.", nil
			}
			if standingSet {

				return "", fmt.Errorf("standing state was updated and persists, but the rest of this call needed a work session and was NOT applied: %w", err)
			}
			return "", err
		}
		if family != nil {
			if err := e.store.SetWorkSessionFamily(ctx, wsID, *family); err != nil {
				return "", err
			}
		}
		if hasCheck {
			if err := e.store.SetWorkCheck(ctx, wsID, check); err != nil {
				return "", err
			}
		}
		if state != nil {
			if err := e.store.UpdateWorkState(ctx, wsID, *state); err != nil {
				return "", err
			}
		}
		if focus != nil || nextMove != nil || plan != nil || expectedEvidence != nil || falsifier != nil || decisionNeeded != nil {
			if err := e.store.UpdateWorkPlan(ctx, wsID, focus, nextMove, plan, expectedEvidence, falsifier, decisionNeeded); err != nil {
				return "", err
			}
		}

		if err := e.putDecision(wsID, decisionNeeded, args); err != nil {
			return "", err
		}
		switch {
		case sessionWork && standingSet:
			return "Work session updated, and standing state with it.", nil
		case sessionWork:
			return "Work session updated.", nil
		case standingSet:
			return "Standing state updated.", nil
		}

		return "Nothing to update — name at least one of state, focus, next_move, plan, family, or standing.", nil

	case "deliver":
		wsID, err := e.currentWorkSessionID(ctx)
		if err != nil {
			return "", err
		}
		result, _ := args["result"].(string)
		evidenceArg, _ := args["evidence"].(string)
		readbackArg, _ := args["evidence_readback"].(string)

		if err := e.workLineFits("evidence_readback", readbackArg); err != nil {
			return "", err
		}

		if !outcomeForm.MatchString(result) {
			return "", rejectf("deliver.outcome_line", "no effect — begin the result with served:, partial: or unserved:, then one line of what happened (e.g. \"partial: tests written, integration blocked\"); nothing was delivered, so correct the outcome line and deliver again")
		}

		if store.IsPreStartFailure(result) {
			return "", rejectf("deliver.reserved_marker", "no effect — a result that begins \"unserved: failed before start:\" is the runtime's record of a run that never started; state the verdict in your own words (e.g. \"unserved: could not read the input\") and deliver again")
		}
		subID, _ := ctx.Value(SubagentWorkSession{}).(string)
		evidence, err := resolveDeliveryEvidence(evidenceArg, readbackArg, result, subID != "")
		if err != nil {
			return "", err
		}

		if err := e.store.DeliverWorkSession(ctx, wsID, result, evidence, readbackArg); err != nil {
			return "", fmt.Errorf("deliver work session: %w", err)
		}
		msg := deliveredLine(e.store, wsID) + " If it mattered, note it — work output becomes identity truth only through your own note, or your own completion of a promise it fulfills."
		if scope := store.RenderEvidenceScope(evidence); scope != "" {
			msg += "\nEvidence: " + scope
		}

		if e.workDelivered != nil {
			msg += e.workDelivered(ctx, wsID)
		}
		return msg, nil

	default:
		return "", withCode("work.action", fmt.Errorf("unknown work action: %s", action))
	}
}

func (e *Engine) workStatus(ctx context.Context) (string, error) {
	state := "Authored work-state readback unavailable: this host has no work-state renderer."
	if e.workState != nil {
		var err error
		state, err = e.workState(ctx)
		if err != nil {
			return "", fmt.Errorf("work status: read authored work state: %w", err)
		}
		if strings.TrimSpace(state) == "" {
			state = "No authored work state is recorded. Use work action=update standing=… to record what outlives a session, or work action=start description=… to begin work."
		}
	}
	unavailable := func(cause error) (string, error) {
		if !e.inSafeMode() {
			return "", fmt.Errorf("work status: %w", cause)
		}

		return e.boundWorkStatus(ctx, state+"\n\nSub-agent rendezvous unavailable in "+e.heldBy().Name()+": "+cause.Error()+". Retained worker state could not be read; do not infer zero running or delivered workers.")
	}
	live, err := e.store.LiveSubagentSessions()
	if err != nil {
		return unavailable(err)
	}
	delivered, err := e.store.UnharvestedDeliveries(10)
	if err != nil {
		return unavailable(err)
	}
	var b strings.Builder
	b.WriteString(state)
	b.WriteString("\n\n")
	fmt.Fprintf(&b, "Sub-agents: %d running, %d delivered awaiting your next compose.\n", len(live), len(delivered))
	for _, w := range live {
		fmt.Fprintf(&b, "- running %s: %s\n", w.ID, store.SubagentGoal(w.Description))
	}
	for _, w := range delivered {
		fmt.Fprintf(&b, "- delivered %s: %s\n", w.ID, store.SubagentGoal(w.Description))
	}
	if len(delivered) > 0 {
		b.WriteString("`work action=status id=…` reads one's delivered result whole.\n")
	}
	if len(live) > 0 && len(delivered) == 0 {
		b.WriteString("If only waiting remains, end the turn — a delivery wakes you. Never sleep or poll: your held turn is what blocks the wake.")
	}
	return e.boundWorkStatus(ctx, strings.TrimRight(b.String(), "\n"))
}

type WorkStatusSizeError struct {
	Required, Limit int
}

func (e *WorkStatusSizeError) Error() string {
	return fmt.Sprintf("rejected_before_effect (status.size): no effect — the whole work status needs %d characters; prompt.max_tool_result_chars allows %d. Nothing was clipped. Ask your operator to raise prompt.max_tool_result_chars to at least %d and restart before reading again", e.Required, e.Limit, e.Required)
}

func (e *Engine) boundWorkStatus(ctx context.Context, out string) (string, error) {
	if !utf8.ValidString(out) {
		return "", rejectf("status.encoding", "no effect — the whole work status contains text that is not valid UTF-8 and cannot be returned verbatim. Nothing was changed; ask your operator to inspect the retained work text before retrying")
	}
	bound := e.ResultLimit(ctx)
	if size := utf8.RuneCountInString(out); size > bound {
		return "", &WorkStatusSizeError{Required: size, Limit: bound}
	}
	return out, nil
}

const workResultLabels = 200

var workResultRef = regexp.MustCompile(`(?i)^(\S+)\s+part\s*[:#]?\s*(\d+)$`)

func (e *Engine) workResult(ctx context.Context, ref string) (string, error) {
	id, partArg := ref, ""
	if m := workResultRef.FindStringSubmatch(ref); m != nil {
		id, partArg = m[1], m[2]
	}
	ws, err := e.store.WorkSessionByID(id)
	if err != nil {
		return "", fmt.Errorf("work status: %w", err)
	}
	if ws == nil || !ws.Child {
		return "", rejectf("status.id", "no effect — %s names none of your sub-agents; a ws_ id from an outcome line or from work action=status names one", id)
	}
	if ws.Status != "delivered" {
		return "", rejectf("status.delivered", "no effect — sub-agent %s has not delivered; its outcome wakes you when it does", id)
	}

	head := fmt.Sprintf("Delivered result of sub-agent %s (%s)", id, store.SubagentGoal(ws.Description))
	scope := store.RenderEvidenceScope(ws.Evidence)
	labels := utf8.RuneCountInString(head) + utf8.RuneCountInString(scope) + workResultLabels
	body, part, parts, ok := partOf(ws.Result, partArg, partRunes(e.ResultLimit(ctx), labels))
	if !ok {
		return "", rejectf("status.part", "no effect — %s's result has %d part(s); part %s is not one of them", id, parts, partArg)
	}
	if parts > 1 {
		head += fmt.Sprintf(", part %d of %d", part, parts)
	}
	if scope != "" {
		head += " " + scope
	}
	out := head + ":\n\n" + body

	out += fmt.Sprintf("\n\n[end of %s's result, part %d of %d", id, part, parts)
	if part < parts {
		return out + fmt.Sprintf("; continues: work action=status id=\"%s part:%d\"]", id, part+1), nil
	}
	return out + "]", nil
}

func resolveDeliveryEvidence(class, readback, result string, workerSeat bool) (string, error) {
	if class == "" {
		if workerSeat {
			return store.EvidenceWorkerReportOnly, nil
		}
		if strings.HasPrefix(result, "served:") {
			return store.EvidenceCompletedLocally, nil
		}
		if strings.HasPrefix(result, "partial:") {
			return store.EvidencePartialOrMixed, nil
		}

		return "", nil
	}
	if !store.IsEvidenceClass(class) {
		return "", rejectf("deliver.evidence", "nothing was delivered. Put one class value in evidence and explanatory text in evidence_readback; class is for project.evidence. Accepted evidence values: not_run, rejected_before_effect, completed_locally, partial_or_mixed, external_effect_unknown, worker_report_only, locally_verified, host_receipted. Correct the fields and deliver again")
	}
	if store.EvidenceVerifiedTier(class) {

		if !strings.HasPrefix(result, "served:") {
			return "", fmt.Errorf("deliver: a %s result must begin served: — you cannot verify an outcome you did not serve; use partial:/unserved: with an unverified class", class)
		}
		if strings.TrimSpace(readback) == "" {
			return "", fmt.Errorf("deliver: evidence %q requires evidence_readback — one line of what you executed and read back; a verified result is a check, not a claim", class)
		}
	}
	return class, nil
}

func (e *Engine) currentWorkSessionID(ctx context.Context) (string, error) {
	if id, ok := ctx.Value(SubagentWorkSession{}).(string); ok && id != "" {
		return id, nil
	}
	ws, err := e.store.ActiveWorkSession()
	if err != nil {
		return "", fmt.Errorf("load active work session: %w", err)
	}
	if ws == nil {

		return "", withCode("work.no_session", fmt.Errorf("no active work session — `work start` opens one (description=what the work is), then update and deliver act on it"))
	}
	return ws.ID, nil
}

func deliveredLine(st workStore, id string) string {
	if ws, err := st.WorkSessionByID(id); err == nil && ws != nil && ws.ToolCallsKnown {
		return fmt.Sprintf("Delivered %s (Ring 4) after %d tool calls.", id, ws.ToolCalls)
	}
	return fmt.Sprintf("Delivered %s (Ring 4).", id)
}
