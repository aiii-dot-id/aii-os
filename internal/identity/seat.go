package identity

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/aiii-dot-id/aii-os/internal/store"
)

type SeatError struct {
	Operation string
	Why       string
}

func (e *SeatError) Error() string { return e.Why }

type seatRule func(e *Engine, ctx context.Context, args map[string]any) (string, error)

func anySeat(*Engine, context.Context, map[string]any) (string, error) { return "", nil }

func primaryOnly(why string) seatRule {
	return func(*Engine, context.Context, map[string]any) (string, error) { return why, nil }
}

var workerSeatTable = map[string]seatRule{
	"note":   anySeat,
	"recall": anySeat,
	"send":   anySeat,
	"tools":  anySeat,

	"work spawn":  anySeat,
	"work status": anySeat,
	"work yield": func(_ *Engine, ctx context.Context, _ map[string]any) (string, error) {
		who := "a sub-agent"
		if depth, _ := ctx.Value(SubagentDepth{}).(int); depth > 0 {
			who = fmt.Sprintf("a sub-agent (depth %d)", depth)
		}
		return "yield is the resident's turn control and you are " + who + ": you hold no gate to free, and yielding here would deliver a fragment. Finish your one deliverable and return it", nil
	},
	"work start": func(_ *Engine, ctx context.Context, _ map[string]any) (string, error) {
		child, _ := ctx.Value(SubagentWorkSession{}).(string)
		return fmt.Sprintf("rejected_before_effect (start.worker_session): no effect — your work session is already %s; use work update or work deliver on that session", child), nil
	},
	"work update": anySeat,
	"work deliver": func(_ *Engine, _ context.Context, args map[string]any) (string, error) {
		if class, _ := args["evidence"].(string); store.EvidenceVerifiedTier(class) {
			return fmt.Sprintf("deliver: a worker seat cannot declare %q — deliver worker_report_only; the primary seat verifies from its own reads", class), nil
		}
		return "", nil
	},
	"work measure":          anySeat,
	"work alarm.set":        anySeat,
	"work alarm.cancel":     anySeat,
	"work project.create":   anySeat,
	"work project.update":   anySeat,
	"work project.close":    anySeat,
	"work project.select":   anySeat,
	"work project.deselect": anySeat,
	"work project.evidence": func(_ *Engine, _ context.Context, args map[string]any) (string, error) {
		class, _ := args["class"].(string)
		if class = strings.TrimSpace(class); store.EvidenceVerifiedTier(class) {
			return fmt.Sprintf("a worker seat cannot mark a project acceptance item %q — only the primary seat verifies; record worker_report_only and let the primary seat verify from its own reads", class), nil
		}
		return "", nil
	},
	"work project.waive":   anySeat,
	"work curiosity":       anySeat,
	"work curiosity.clear": anySeat,
	"work voice.mode":      anySeat,
	"work backup.take":     primaryOnly(snapshotIsTheMainSeats),
	"work backup.verify":   primaryOnly(snapshotIsTheMainSeats),

	"commit belief.upsert":           anySeat,
	"commit belief.attest":           anySeat,
	"commit belief.promote":          primaryOnly("a worker seat cannot promote to Ring 2 — who the identity is is the primary seat's to author; report the evidence and let the primary seat promote"),
	"commit belief.archive":          ring2Target("id"),
	"commit belief.supersede":        ring2Target("old_id"),
	"commit relationship.upsert":     primaryOnly("a worker seat cannot author Ring 1 — the charter is the primary seat's to say to the operator and to record once affirmed"),
	"commit self_model.synthesize":   anySeat,
	"commit edge.create":             anySeat,
	"commit edge.archive":            anySeat,
	"commit intention.create":        anySeat,
	"commit intention.state_change":  anySeat,
	"commit commitment.promised":     anySeat,
	"commit commitment.state_change": anySeat,
	"commit working_style.upsert":    anySeat,
	"commit skill.propose":           anySeat,
}

const snapshotIsTheMainSeats = "a snapshot of you is yours to take, not a worker's: say in your result that one is wanted, and the main seat takes it (recall source=continuity reads the state from any seat)"

func ring2Target(key string) seatRule {
	return func(e *Engine, _ context.Context, args map[string]any) (string, error) {
		id, _ := args[key].(string)
		if id == "" {
			return "", nil
		}
		b, err := e.store.GetBelief(id)
		if errors.Is(err, store.ErrBeliefNotFound) {
			return "", nil
		}
		if err != nil {
			return "", fmt.Errorf("a worker seat may not remove a Ring 2 belief, and the ring of %q cannot be read to tell (%w); nothing was changed", id, err)
		}
		if b.Ring == 2 {
			return fmt.Sprintf("a worker seat cannot archive or supersede a Ring 2 belief — who the identity is is the primary seat's to author; report why %q should go and let the primary seat decide", id), nil
		}
		return "", nil
	}
}

func inWorkerSeat(ctx context.Context) bool {
	if _, ok := ctx.Value(SubagentWorkSession{}).(string); ok {
		return true
	}
	depth, _ := ctx.Value(SubagentDepth{}).(int)
	return depth > 0
}

func operation(verb string, args map[string]any) string {
	switch verb {
	case "work":
		action, _ := args["action"].(string)
		if action == "" {
			action = "start"
		}
		return "work " + action
	case "commit":
		variant, _ := args["variant"].(string)
		return "commit " + variant
	}
	return verb
}

func (e *Engine) workerMay(ctx context.Context, verb string, args map[string]any) error {
	op := operation(verb, args)
	rule, listed := workerSeatTable[op]
	if !listed {
		return &SeatError{Operation: op, Why: fmt.Sprintf("%s is not among the operations a worker seat may call — it is the primary seat's: say in your result what should be done and why, and the primary seat decides", op)}
	}
	why, err := rule(e, ctx, args)
	if err != nil {
		return &SeatError{Operation: op, Why: err.Error()}
	}
	if why != "" {
		return &SeatError{Operation: op, Why: why}
	}
	return nil
}
