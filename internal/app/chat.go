package app

import (
	"context"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/interaction"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"strings"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/conversation"
	"github.com/aiii-dot-id/aii-os/internal/llm"
)

func replayContent(role, content string) string {
	if role == "system" {
		return replayView(content)
	}
	return content
}

func replayView(content string) string {

	const head, tail = 600, 300
	marker := "\n← "
	nl := strings.Index(content, marker)
	if nl < 0 {
		runes := []rune(content)
		if len(runes) > head+tail {
			return string(runes[:head]) + "\n…[trimmed for replay]…\n" + string(runes[len(runes)-tail:])
		}
		return content
	}
	call := content[:nl]
	result := content[nl+len(marker):]
	callRunes := []rune(call)
	if len(callRunes) > head {
		call = string(callRunes[:head]) + "…"
	}
	resultRunes := []rune(result)
	if len(resultRunes) > head+tail {
		result = string(resultRunes[:head]) + "\n…[result trimmed for replay]…\n" + string(resultRunes[len(resultRunes)-tail:])
	}
	return call + marker + result
}

func (a *App) observeChat(ctx context.Context, msg string, emit func(kind, name, args string)) (string, error) {
	defer a.releaseTurn()
	a.resetAsk()
	ctx, cancel := a.beginCancellableTurn(ctx)
	defer cancel()
	a.toolEmitMu.Lock()
	a.toolEmit = emit
	a.toolEmitMu.Unlock()
	defer func() {
		a.toolEmitMu.Lock()
		a.toolEmit = nil
		a.toolEmitMu.Unlock()
	}()
	reply, err := a.handleMessageLocked(ctx, msg)
	if a.voiceReplyShown.Swap(false) {

		return "", err
	}
	return reply, err
}

func (a *App) handleMessage(ctx context.Context, msg string) (string, error) {
	if err := a.acquireTurn(ctx); err != nil {
		return "", err
	}
	defer a.releaseTurn()
	ctx, cancel := a.beginCancellableTurn(ctx)
	defer cancel()
	return a.handleMessageLocked(ctx, msg)
}

func (a *App) handleMessageLocked(ctx context.Context, msg string) (string, error) {
	ctx = withTurnSource(ctx, turnSourceOperator)
	ref, err := a.recordConversationRef(ctx, "operator", msg)
	seq := ref.Sequence
	if err != nil {
		return "", fmt.Errorf("record operator turn: %w", err)
	}
	a.tagSpokenTurn(seq, msg)
	return a.runTurnLocked(ctx, msg)
}

func (a *App) runTurnLocked(ctx context.Context, msg string) (string, error) {
	reply, err := a.runTurnLockedInner(ctx, msg)

	a.settleVoice(ctx, reply)
	return reply, err
}

func (a *App) runTurnLockedInner(ctx context.Context, msg string) (string, error) {
	current := llm.Message{Role: "user", Content: msg}
	conv, omitted, err := a.buildHistory()
	if err != nil {
		return "", err
	}
	workState, err := a.buildWorkState()
	if err != nil {
		return "", err
	}

	facts, err := a.buildTurnFacts(true)
	if err != nil {
		return "", err
	}
	reserve, err := a.promptReserve(current, omitted+len(conv)-1)
	if err != nil {
		return "", err
	}
	p, err := a.composer.ComposeTurn(workState, facts, a.safeTurnSection(), reserve)
	if err != nil {
		return "", fmt.Errorf("prompt compose: %w", err)
	}

	a.carriedEvents = a.eventsCarriedBy(p.Turn)
	result, err := a.conv.RunTurn(ctx, a.gatedSystem(p), conv, omitted, p.Turn)
	if err != nil {

		a.recordInterruptedTurnContext(ctx, result)
		return "", err
	}
	a.recordTurnCost(ctx, result)
	if result.Usage.Calls > 0 {
		a.recordTurnEventDelivery(ctx, p.Turn)
	}

	a.markComposedHarvests()
	a.noteYield(result.Yielded)
	a.noteTurnShape(result.ContinuedAtCap || result.ContinuedAtPressure, result.ContinuedAtPressure)
	finalText := result.FinalText

	if result.Spoken != "" {
		if _, err := a.recordReply(ctx, result.Spoken, result); err != nil {
			return "", fmt.Errorf("record resident turn: %w", err)
		}
		return result.Spoken, nil
	}

	if finalText != "" {
		if _, err := a.recordReply(ctx, finalText, result); err != nil {
			return "", fmt.Errorf("record resident turn: %w", err)
		}
	}
	return finalText, nil
}

func (a *App) recordInterruptedTurn(result conversation.Result) {
	a.recordInterruptedTurnContext(context.Background(), result)
}
func (a *App) recordInterruptedTurnContext(ctx context.Context, result conversation.Result) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	a.recordTurnCost(ctx, result)
	if result.Spoken == "" {
		return
	}
	ref, err := a.recordReply(ctx, result.Spoken, result)
	if err != nil {
		logsink.Warn("turn.error", "interrupted turn not recorded: %v", err)
		return
	}
	if _, err := a.engine.RecordRelatedConversationRef(ctx, "system",
		"[the reply above is incomplete — "+result.Interrupted+"]", ref.ID, interaction.Details{ReasonCode: "interrupted"}); err != nil {
		logsink.Warn("turn.error", "interruption marker not recorded: %v", err)
	}
}
