package identity

import (
	"context"
	"fmt"
	"strings"
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
// .
// .
// .
// .
// .
// .

// .
type ContinuityPort interface {
	// .
	// .
	// .
	Read(ctx context.Context, query string) (string, error)
	// .
	// .
	// .
	Take(ctx context.Context) (string, error)
	// .
	// .
	Verify(ctx context.Context, name string) (string, error)
}

// .
// .
func (e *Engine) SetContinuity(c ContinuityPort) { e.continuity = c }

// .
const (
	continuityRead   = "read"
	continuityTake   = "take"
	continuityVerify = "verify"
)

// .
// .
func (e *Engine) verbContinuity(ctx context.Context, args map[string]interface{}) (string, error) {
	if e.continuity == nil {
		return "", fmt.Errorf("this runtime keeps no snapshots of you: nothing here takes, proves or lists them")
	}
	action, _ := args["action"].(string)
	switch action {
	case continuityRead:
		query, _ := args["query"].(string)
		return e.continuity.Read(ctx, strings.TrimSpace(query))
	case continuityTake, continuityVerify:
		// .
		// .
		// .
		// .
		if d, ok := ctx.Value(SubagentDepth{}).(int); ok && d > 0 {
			return "", fmt.Errorf("a snapshot of you is yours to take, not a worker's: say in your result that one is wanted, and the main seat takes it (recall source=continuity reads the state from any seat)")
		}
		if action == continuityTake {
			return e.continuity.Take(ctx)
		}
		name, _ := args["id"].(string)
		return e.continuity.Verify(ctx, strings.TrimSpace(name))
	}
	return "", fmt.Errorf("continuity has three modes — recall source=continuity reads it, work action=backup.take makes a snapshot, work action=backup.verify proves one; %q is not one of them, and what is not here (your keys' escrow, a restore, what is kept and for how long) is your operator's to do", action)
}
