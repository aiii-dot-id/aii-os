package app

import (
	"context"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/interaction"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/store"
	"strings"

	"github.com/aiii-dot-id/aii-os/internal/conversation"
	"github.com/aiii-dot-id/aii-os/internal/llm"
)

func (a *App) wake(ctx context.Context, role, fact string) (string, error) {
	var reply string
	var err error
	if role == string(interaction.Operator) {

		reply, err = a.operatorTurn(ctx, fact)
	} else {
		reply, err = a.wakeInner(ctx, role, fact)
	}
	a.settleVoice(ctx, reply)
	return reply, err
}

type turnError struct {
	recorded bool
	replied  bool
	err      error
}

func (e *turnError) Error() string { return e.err.Error() }
func (e *turnError) Unwrap() error { return e.err }

func unrecorded(err error) error { return &turnError{err: err} }

func (a *App) operatorTurn(ctx context.Context, msg string) (string, error) {
	if a.conv == nil || a.composer == nil || a.engine == nil {
		return "", unrecorded(fmt.Errorf("the identity is not live"))
	}
	a.resetAsk()
	ctx, cancel := a.beginCancellableTurn(ctx)
	defer cancel()
	defer a.withToolEmit(a.pageToolEmit())()
	return a.handleMessageLocked(ctx, msg)
}

func (a *App) wakeInner(ctx context.Context, role, fact string) (string, error) {

	if a.conv == nil || a.composer == nil || a.engine == nil {
		return "", unrecorded(fmt.Errorf("the identity is not live"))
	}
	ctx, cancel := a.beginCancellableTurn(ctx)
	defer cancel()

	if err := a.recordConversation(ctx, role, fact); err != nil {
		return "", unrecorded(fmt.Errorf("record the fact: %w", err))
	}

	result, err := a.think(ctx, llm.Message{Role: "user", Content: fact}, true)
	if err != nil {
		return "", err
	}
	spoken := result.Spoken
	if spoken == "" {
		spoken = result.FinalText
	}
	if strings.TrimSpace(spoken) == "" {

		return "", nil
	}

	return a.answer(ctx, spoken, result)
}

func (a *App) think(ctx context.Context, current llm.Message, appendCurrent bool) (conversation.Result, error) {
	workState, err := a.buildWorkState()
	if err != nil {
		return conversation.Result{}, fmt.Errorf("working state: %w", err)
	}

	firing, _ := ctx.Value(timerRecordKey{}).(string)
	facts, err := a.buildTurnFacts(ctx, true, firing)
	if err != nil {
		return conversation.Result{}, fmt.Errorf("turn facts: %w", err)
	}
	conv, omitted, err := a.buildHistory(ctx)
	if err != nil {
		return conversation.Result{}, fmt.Errorf("history: %w", err)
	}
	history := conv
	if n := len(conv); appendCurrent && (n == 0 || conv[n-1].Role != "user" || conv[n-1].Content != current.Content) {
		history = append(conv, current)
	}
	reserve, err := a.promptReserve(current, omitted+len(history)-1)
	if err != nil {
		return conversation.Result{}, fmt.Errorf("request estimate: %w", err)
	}
	p, err := a.composer.ComposeTurn(workState, facts, a.safeTurnSection(), reserve)
	if err != nil {
		return conversation.Result{}, fmt.Errorf("compose: %w", err)
	}

	a.carriedEvents = a.eventsCarriedBy(p.Turn)
	result, err := a.conv.RunTurn(ctx, a.gatedSystem(p), history, omitted, p.Turn)
	if err != nil {

		a.recordInterruptedTurnContext(ctx, result)
		return result, err
	}
	a.recordTurnCost(ctx, result)
	if result.Usage.Calls > 0 {
		a.recordTurnEventDelivery(ctx, p.Turn)
		if firing != "" {
			if err := a.store.RecordTurnEvents(ctx, []store.TurnEvent{{Timer: &store.OutboxMessage{ID: firing}, Text: current.Content}}, interaction.TurnID(ctx)); err != nil {
				logsink.Warn("prompt.error", "timer input acknowledgment failed; its notice remains pending: %v", err)
			}
		}
	}

	a.markComposedHarvests()
	a.noteYield(result.Yielded)
	a.noteTurnShape(result.ContinuedAtCap || result.ContinuedAtPressure, result.ContinuedAtPressure)
	return result, nil
}
