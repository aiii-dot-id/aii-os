package identity

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/aiii-dot-id/aii-os/internal/store"
	"github.com/google/uuid"
)

const SubagentWorkKind = "subagent.run"

type SubagentRequest struct {
	Goal           string `json:"goal"`
	Depth          int    `json:"depth"`
	SessionID      string `json:"ws_id"`
	ThinkingBudget int    `json:"thinking_budget"`
	Context        string `json:"context"`
	ParentBudget   int    `json:"parent_budget"`
	Role           string `json:"role"`
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	WallSeconds int `json:"wall_seconds,omitempty"`
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	Leg       int   `json:"leg,omitempty"`
	AccCalls  int   `json:"acc_calls,omitempty"`
	AccTokens int   `json:"acc_tokens,omitempty"`
	AccWallMs int64 `json:"acc_wall_ms,omitempty"`
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

// .

func (e *Engine) verbWork(ctx context.Context, args map[string]interface{}) (string, error) {
	action, _ := args["action"].(string)
	if action == "" {
		action = "start"
	}

	// .
	// .
	// .
	// .
	if out, handled, err := e.workAbsorbed(ctx, action, args); handled {
		return out, err
	}

	switch action {
	case "spawn":
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		goal, _ := args["goal"].(string)
		if goal == "" {
			goal, _ = args["_positional"].(string)
		}
		if goal == "" {
			return "", fmt.Errorf("spawn requires a goal")
		}
		depth := 0
		if d, ok := args["_subagent_depth"].(int); ok {
			depth = d
		}
		// .
		// .
		// .
		maxDepth, maxParallel, _, _ := e.agencyLimits()
		queueOn := e.spawnQueueOn()
		bound := maxParallel
		if queueOn {
			bound = maxParallel * 2
		}
		if maxDepth <= 0 {
			return "", fmt.Errorf("spawn refused: sub-agents are disabled (agency.max_subagent_depth = 0) — ask your operator to enable them")
		}
		if depth+1 > maxDepth {
			return "", fmt.Errorf("spawn refused: depth %d would exceed agency.max_subagent_depth %d — finish this level's goal or ask your operator to raise the ceiling", depth+1, maxDepth)
		}
		wsID := "ws_" + uuid.New().String()
		// .
		// .
		// .
		// .
		// .
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
		// .
		// .
		// .
		// .
		wallSeconds := e.spawnWall(role)
		request := SubagentRequest{
			Goal: goal, Depth: depth + 1, SessionID: wsID,
			ThinkingBudget: thinking, Context: ctxScope,
			ParentBudget: parentBudget, Role: role,
			WallSeconds: wallSeconds,
		}
		payload, err := json.Marshal(request)
		if err != nil {
			return "", fmt.Errorf("encode subagent request: %w", err)
		}
		// .
		// .
		// .
		// .
		leaseMs := int64(wallSeconds+60) * 1000
		live, enqueued, err := e.store.EnqueueWorkWithSessionBelowLimit(&store.WorkItem{
			Kind: SubagentWorkKind, Payload: string(payload), DedupKey: wsID, Source: "identity",
			LeaseMs: leaseMs,
		}, bound, wsID, store.SubagentDescription(goal))
		if err != nil {
			return "", fmt.Errorf("spawn enqueue: %w", err)
		}
		if !enqueued {
			if queueOn {
				return "", fmt.Errorf("spawn refused: %d sub-agents queued or running — the queue holds at most %d, twice agency.max_parallel_subagents (%d); harvest what is delivered or wait for a slot", live, bound, maxParallel)
			}
			return "", fmt.Errorf("spawn refused: %d sub-agents already live — agency.max_parallel_subagents is %d; wait for one to finish or ask your operator to raise it", live, maxParallel)
		}
		if e.workWake != nil {
			e.workWake()
		}
		if queueOn && live >= maxParallel {
			// .
			// .
			// .
			return fmt.Sprintf("Queued sub-agent %s (depth %d): %s — %d ahead of it for %d slot(s) (agency.max_parallel_subagents); it starts when a slot frees. Its outcome returns to your working state (Ring 4) and wakes you to harvest it; record this ID in your plan if it belongs to one. Do not spawn it again; keep doing independent work.", wsID, depth+1, goal, live, maxParallel) + e.spawnBudgetLine(wallSeconds), nil
		}
		return fmt.Sprintf("Spawned sub-agent %s (depth %d): %s — its outcome returns to your working state (Ring 4) and wakes you to harvest it; record this ID in your plan if it belongs to one. Note what deserves to become memory.", wsID, depth+1, goal) + e.spawnBudgetLine(wallSeconds), nil

	case "yield":
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
		if depth, _ := args["_subagent_depth"].(int); depth > 0 {
			return "", fmt.Errorf("yield is the resident's turn control and you are a sub-agent (depth %d): you hold no gate to free, and yielding here would deliver a fragment. Finish your one deliverable and return it", depth)
		}
		// .
		// .
		// .
		// .
		// .
		// .
		answer, _ := args["answer"].(string)
		answer = strings.TrimSpace(answer)
		e.agencyMu.RLock()
		gate := e.yieldGate
		e.agencyMu.RUnlock()
		if gate != nil && answer == "" {
			if need, why := gate(); need {
				return "", fmt.Errorf("yield refused: %s. State the best current answer for the operator — answer= (what is established, what is missing, what would change it) — then yield", why)
			}
		}
		if answer != "" {
			return "Turn yielding with your answer carried to the operator — your gate frees when this round ends. Your session stays active; a delivery or message wakes you.", nil
		}
		return "Turn yielding — your gate frees when this round ends. Your session stays active; a delivery or message wakes you.", nil

	case "status":
		// .
		// .
		// .
		// .
		// .
		live, err := e.store.LiveSubagentSessions()
		if err != nil {
			return "", fmt.Errorf("work status: %w", err)
		}
		delivered, err := e.store.UnharvestedDeliveries(10)
		if err != nil {
			return "", fmt.Errorf("work status: %w", err)
		}
		var b strings.Builder
		fmt.Fprintf(&b, "Sub-agents: %d running, %d delivered awaiting your next compose.\n", len(live), len(delivered))
		for _, w := range live {
			fmt.Fprintf(&b, "- running %s: %s\n", w.ID, store.SubagentGoal(w.Description))
		}
		for _, w := range delivered {
			fmt.Fprintf(&b, "- delivered %s: %s\n", w.ID, store.SubagentGoal(w.Description))
		}
		if len(live) > 0 && len(delivered) == 0 {
			b.WriteString("If only waiting remains, end the turn — a delivery wakes you. Never sleep or poll: your held turn is what blocks the wake.")
		}
		return strings.TrimRight(b.String(), "\n"), nil

	case "start":
		desc, _ := args["description"].(string)
		if desc == "" {
			desc, _ = args["_positional"].(string)
		}
		wsID := "ws_" + uuid.New().String()

		// .
		// .
		if err := e.store.StartWorkSession(wsID, desc); err != nil {
			return "", err
		}

		return fmt.Sprintf("Started work session: %s", desc), nil

	case "update":
		// .
		// .
		// .
		// .
		// .
		standingSet := false
		if v, ok := args["standing"].(string); ok {
			if err := e.store.SetStandingState(v); err != nil {
				return "", fmt.Errorf("standing state not recorded: %w", err)
			}
			standingSet = true
		}
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		var state, focus, nextMove, plan, expectedEvidence, falsifier, decisionNeeded *string
		if v, ok := args["state"].(string); ok {
			state = &v
		} else if v, ok := args["_positional"].(string); ok {
			state = &v
		}
		// .
		// .
		// .
		// .
		if v, ok := args["focus"].(string); ok {
			focus = &v
		}
		if v, ok := args["next_move"].(string); ok {
			nextMove = &v
		}
		if v, ok := args["plan"].(string); ok {
			plan = &v
		}
		if v, ok := args["expected_evidence"].(string); ok {
			expectedEvidence = &v
		}
		if v, ok := args["falsifier"].(string); ok {
			falsifier = &v
		}
		if v, ok := args["decision_needed"].(string); ok {
			decisionNeeded = &v
		}
		sessionWork := state != nil || focus != nil || nextMove != nil || plan != nil || expectedEvidence != nil || falsifier != nil || decisionNeeded != nil
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		for _, k := range []string{"steps", "independent"} {
			if _, ok := args[k]; ok {
				sessionWork = true
			}
		}

		wsID, err := e.currentWorkSessionID(ctx)
		if err != nil {
			// .
			// .
			// .
			// .
			if standingSet && !sessionWork {
				return "Standing state updated.", nil
			}
			if standingSet {
				// .
				// .
				// .
				// .
				// .
				// .
				return "", fmt.Errorf("standing state was updated and persists, but the rest of this call needed a work session and was NOT applied: %w", err)
			}
			return "", err
		}
		if state != nil {
			if err := e.store.UpdateWorkState(wsID, *state); err != nil {
				return "", err
			}
		}
		if focus != nil || nextMove != nil || plan != nil || expectedEvidence != nil || falsifier != nil || decisionNeeded != nil {
			if err := e.store.UpdateWorkPlan(wsID, focus, nextMove, plan, expectedEvidence, falsifier, decisionNeeded); err != nil {
				return "", err
			}
		}
		// .
		// .
		// .
		// .
		if decisionNeeded != nil && e.askProposer != nil {
			var choices []string
			if raw, ok := args["choices"].([]interface{}); ok {
				for _, c := range raw {
					if s, ok := c.(string); ok && strings.TrimSpace(s) != "" {
						choices = append(choices, strings.TrimSpace(s))
					}
				}
			}
			connector, _ := args["connector"].(string)
			if perr := e.askProposer(wsID, *decisionNeeded, choices, strings.TrimSpace(connector)); perr != nil {
				return "", fmt.Errorf("the decision was stored but could not be put to your operator: %w", perr)
			}
		}
		switch {
		case sessionWork && standingSet:
			return "Work session updated, and standing state with it.", nil
		case sessionWork:
			return "Work session updated.", nil
		case standingSet:
			return "Standing state updated.", nil
		}
		// .
		// .
		return "Nothing to update — name at least one of state, focus, next_move, plan, or standing.", nil

	case "deliver":
		wsID, err := e.currentWorkSessionID(ctx)
		if err != nil {
			return "", err
		}
		result, _ := args["result"].(string)
		evidenceArg, _ := args["evidence"].(string)
		readbackArg, _ := args["evidence_readback"].(string)
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		if !outcomeForm.MatchString(result) {
			return "", fmt.Errorf("rejected_before_effect (deliver.outcome_line): no effect — begin the result with served:, partial: or unserved:, then one line of what happened (e.g. \"partial: tests written, integration blocked\"); nothing was delivered, so correct the outcome line and deliver again")
		}
		subID, _ := ctx.Value(SubagentWorkSession{}).(string)
		evidence, err := resolveDeliveryEvidence(evidenceArg, readbackArg, result, subID != "")
		if err != nil {
			return "", err
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
		if commitmentID, ok := args["commitment_id"].(string); ok && commitmentID != "" {
			commitments, err := e.store.ListCommitments(true)
			if err != nil {
				return "", fmt.Errorf("deliver: list commitments: %w", err)
			}
			found := false
			for _, c := range commitments {
				if c.ID == commitmentID {
					found = true
					break
				}
			}
			if !found {
				return "", fmt.Errorf("rejected_before_effect (deliver.commitment_id): no effect — commitment %s not found among active commitments; nothing was delivered", commitmentID)
			}
			if err := e.store.DeliverWorkSession(wsID, result, evidence, readbackArg); err != nil {
				return "", fmt.Errorf("deliver work session: %w", err)
			}
			msg := fmt.Sprintf("Delivered (Ring 4), naming commitment %s. If this delivery fulfills it, the completion is yours to write: commit commitment.state_change — a promise kept is identity truth only when YOU complete it.", commitmentID)
			if scope := store.RenderEvidenceScope(evidence); scope != "" {
				msg += "\nEvidence: " + scope
			}
			return msg, nil
		}

		if err := e.store.DeliverWorkSession(wsID, result, evidence, readbackArg); err != nil {
			return "", fmt.Errorf("deliver work session: %w", err)
		}
		msg := "Delivered (Ring 4). If it mattered, note it — work output becomes identity truth only through your own note, or your own completion of a promise it fulfills."
		if scope := store.RenderEvidenceScope(evidence); scope != "" {
			msg += "\nEvidence: " + scope
		}
		return msg, nil

	default:
		return "", fmt.Errorf("unknown work action: %s", action)
	}
}

// .
// .
// .
// .
// .
// .
// .
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
		// .
		// .
		// .
		return "", nil
	}
	if !store.IsEvidenceClass(class) {
		return "", fmt.Errorf("deliver: unknown evidence class %q — one of: not_run, rejected_before_effect, completed_locally, partial_or_mixed, external_effect_unknown, worker_report_only, locally_verified, host_receipted", class)
	}
	if store.EvidenceVerifiedTier(class) {
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		if !strings.HasPrefix(result, "served:") {
			return "", fmt.Errorf("deliver: a %s result must begin served: — you cannot verify an outcome you did not serve; use partial:/unserved: with an unverified class", class)
		}
		if workerSeat {
			return "", fmt.Errorf("deliver: a worker seat cannot declare %q — deliver worker_report_only; the primary seat verifies from its own reads", class)
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
		return "", fmt.Errorf("no active work session")
	}
	return ws.ID, nil
}
