package app

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/aiii-dot-id/aii-os/internal/llm"
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
func (a *App) wake(ctx context.Context, role, fact string) (string, error) {
	var reply string
	var err error
	if role == roleOperator {
		// .
		// .
		// .
		reply, err = a.operatorTurn(ctx, fact)
	} else {
		reply, err = a.wakeInner(ctx, role, fact)
	}
	a.settleVoice(ctx, reply)
	return reply, err
}

// .
// .
func (a *App) operatorTurn(ctx context.Context, msg string) (string, error) {
	if a.conv == nil || a.composer == nil || a.engine == nil {
		return "", fmt.Errorf("the identity is not live")
	}
	a.resetAsk()
	ctx, cancel := a.beginCancellableTurn(ctx)
	defer cancel()
	emit := a.pageToolEmit()
	a.toolEmitMu.Lock()
	a.toolEmit = emit
	a.toolEmitMu.Unlock()
	defer func() {
		a.toolEmitMu.Lock()
		a.toolEmit = nil
		a.toolEmitMu.Unlock()
	}()
	return a.handleMessageLocked(ctx, msg)
}

func (a *App) wakeInner(ctx context.Context, role, fact string) (string, error) {
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
	if a.conv == nil || a.composer == nil || a.engine == nil {
		return "", fmt.Errorf("the identity is not live")
	}
	ctx, cancel := a.beginCancellableTurn(ctx)
	defer cancel()

	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	if err := a.engine.RecordConversationTurn(role, fact); err != nil {
		return "", fmt.Errorf("record the fact: %w", err)
	}
	workState, err := a.buildWorkState()
	if err != nil {
		return "", fmt.Errorf("working state: %w", err)
	}
	conv, omitted, err := a.buildHistory()
	if err != nil {
		return "", fmt.Errorf("history: %w", err)
	}
	current := llm.Message{Role: "user", Content: fact}
	reserve, err := a.promptReserve(current, omitted+len(conv))
	if err != nil {
		return "", fmt.Errorf("request estimate: %w", err)
	}
	p, err := a.composer.Compose(workState, reserve)
	if err != nil {
		return "", fmt.Errorf("compose: %w", err)
	}

	result, err := a.conv.RunSystem(ctx, a.gatedSystem(p), append(conv, current), omitted)
	if err != nil {
		a.recordInterruptedTurn(result)
		return "", err
	}
	a.recordTurnCost(result.Usage)
	// .
	// .
	// .
	// .
	// .
	a.markComposedHarvests()
	a.noteYield(result.Yielded)
	a.noteTurnShape(result.ContinuedAtCap)

	spoken := result.Spoken
	if spoken == "" {
		spoken = result.FinalText
	}
	if strings.TrimSpace(spoken) == "" {
		// .
		return "", nil
	}
	if err := a.engine.RecordConversationTurn("resident", spoken); err != nil {
		log.Printf("FAILED to record wake speech: %v — the transcript is missing what was said", err)
	}
	return spoken, nil
}
