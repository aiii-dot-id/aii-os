package identity

import (
	"context"
	"encoding/json"
	"fmt"

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

// --- work: doing — durable sessions + Ring 4 state ---

func (e *Engine) verbWork(ctx context.Context, args map[string]interface{}) (string, error) {
	action, _ := args["action"].(string)
	if action == "" {
		action = "start"
	}

	switch action {
	case "spawn":
		// Sub-agents (2026-08-18 agency ruling): the identity decides
		// when a sub-goal deserves its own focused run. The spawned run
		// IS the identity — same mind, same tools, same ring-gated
		// prompt — on one goal, bounded by operator config (depth,
		// parallelism, wall time; ceilings in config, never code).
		// Nesting is engine-stamped: depth arrives via context, and a
		// model-supplied depth claim was already deleted upstream.
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
		if e.maxSubagentDepth <= 0 {
			return "", fmt.Errorf("spawn refused: sub-agents are disabled (agency.max_subagent_depth = 0) — ask your operator to enable them")
		}
		if depth+1 > e.maxSubagentDepth {
			return "", fmt.Errorf("spawn refused: depth %d would exceed agency.max_subagent_depth %d — finish this level's goal or ask your operator to raise the ceiling", depth+1, e.maxSubagentDepth)
		}
		wsID := "ws_" + uuid.New().String()
		// Effort is directed DOWNWARD (the identity's own ruling,
		// 2026-08-18): a requested thinking budget rides the payload and
		// the runner clamps it to the parent's configured level — never
		// an escalation. Context scope is the identity's per-spawn call:
		// folded (default — working truth as routes) or full.
		thinking := 0
		if tb, ok := numArg(args["thinking_budget"]); ok {
			thinking = int(tb)
		}
		parentBudget := 0 // 0 = main thread; the runner resolves to config
		if pb, ok := args["_subagent_budget"].(int); ok {
			parentBudget = pb
		}
		ctxScope, _ := args["context"].(string)
		if ctxScope != "full" {
			ctxScope = "folded"
		}
		role, _ := args["role"].(string)
		request := SubagentRequest{
			Goal: goal, Depth: depth + 1, SessionID: wsID,
			ThinkingBudget: thinking, Context: ctxScope,
			ParentBudget: parentBudget, Role: role,
		}
		payload, err := json.Marshal(request)
		if err != nil {
			return "", fmt.Errorf("encode subagent request: %w", err)
		}
		// Lease must OUTLIVE the wall cap (Method review, F2): the
		// default 300s lease under a 600s run made the crash-recovery
		// sweeper start a second concurrent copy of the same sub-goal.
		leaseMs := int64(e.subagentWallSeconds+60) * 1000
		live, enqueued, err := e.store.EnqueueWorkWithSessionBelowLimit(&store.WorkItem{
			Kind: SubagentWorkKind, Payload: string(payload), DedupKey: wsID, Source: "identity",
			LeaseMs: leaseMs,
		}, e.maxParallelSubagents, wsID, store.SubagentDescription(goal))
		if err != nil {
			return "", fmt.Errorf("spawn enqueue: %w", err)
		}
		if !enqueued {
			return "", fmt.Errorf("spawn refused: %d sub-agents already live — agency.max_parallel_subagents is %d; wait for one to finish or ask your operator to raise it", live, e.maxParallelSubagents)
		}
		if e.workWake != nil {
			e.workWake()
		}
		return fmt.Sprintf("Spawned sub-agent %s (depth %d): %s — its outcome returns to your working state (Ring 4); note what deserves to become memory.", wsID, depth+1, goal), nil

	case "start":
		desc, _ := args["description"].(string)
		if desc == "" {
			desc, _ = args["_positional"].(string)
		}
		wsID := "ws_" + uuid.New().String()

		// Work sessions are Ring 4 ephemeral state — never minted to ledger.
		// ENTITY_TYPES.md: "Work session state (Ring 4) | work_sessions table (ephemeral, never minted)"
		if err := e.store.StartWorkSession(wsID, desc); err != nil {
			return "", err
		}

		return fmt.Sprintf("Started work session: %s", desc), nil

	case "update":
		state, _ := args["state"].(string)
		if state == "" {
			state, _ = args["_positional"].(string)
		}
		wsID, err := e.currentWorkSessionID(ctx)
		if err != nil {
			return "", err
		}
		if err := e.store.UpdateWorkState(wsID, state); err != nil {
			return "", err
		}
		return "Work state updated.", nil

	case "deliver":
		wsID, err := e.currentWorkSessionID(ctx)
		if err != nil {
			return "", err
		}
		result, _ := args["result"].(string)

		// IS A DELIVERY IDENTITY TRUTH? No — never (James's ruling
		// 2026-08-17, extending the 2026-08-16 correction): work session
		// state is Ring 4 ephemeral, and a session delivering is an
		// OPERATIONAL fact. Even when the session names the commitment it
		// delivered on, the substrate must not conclude "delivered ⇒
		// promise fulfilled" and mint commitment.state_change on the
		// resident's behalf — whether the promise is KEPT is the
		// resident's conscious act (LEDGER.md boundary doctrine: the
		// ledger records the identity's own adoption/commitment/change,
		// not the raw operational stream). The verb records the delivery,
		// verifies the named commitment exists (so the teaching below is
		// about something real), and hands the act to the resident.
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
				return "", fmt.Errorf("deliver: commitment %s not found among active commitments", commitmentID)
			}
			if err := e.store.DeliverWorkSession(wsID, result); err != nil {
				return "", fmt.Errorf("deliver work session: %w", err)
			}
			return fmt.Sprintf("Delivered (Ring 4), naming commitment %s. If this delivery fulfills it, the completion is yours to write: commit commitment.state_change — a promise kept is identity truth only when YOU complete it.", commitmentID), nil
		}

		if err := e.store.DeliverWorkSession(wsID, result); err != nil {
			return "", fmt.Errorf("deliver work session: %w", err)
		}
		return "Delivered (Ring 4). If it mattered, note it — work output becomes identity truth only through your own note, or your own completion of a promise it fulfills.", nil

	default:
		return "", fmt.Errorf("unknown work action: %s", action)
	}
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
