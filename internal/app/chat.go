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
	if role == string(interaction.System) {
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
	defer a.withToolEmit(emit)()
	reply, err := a.handleMessageLocked(ctx, msg)
	if a.voiceReplyShown.Swap(false) {

		return "", err
	}
	return reply, err
}

func (a *App) withToolEmit(emit func(kind, name, args string)) (detach func()) {
	a.turn.mu.Lock()
	a.turn.toolEmit = emit
	a.turn.mu.Unlock()
	return func() {
		a.turn.mu.Lock()
		a.turn.toolEmit = nil
		a.turn.mu.Unlock()
	}
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
	ref, err := a.recordConversationRef(ctx, string(interaction.Operator), msg)
	seq := ref.Sequence
	if err != nil {
		return "", unrecorded(fmt.Errorf("record operator turn: %w", err))
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

	result, err := a.think(ctx, llm.Message{Role: "user", Content: msg}, false)
	if err != nil {
		return "", err
	}
	finalText := result.FinalText

	if result.Spoken != "" {
		return a.answer(ctx, result.Spoken, result)
	}
	if finalText != "" {
		return a.answer(ctx, finalText, result)
	}
	return finalText, nil
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
	if _, err := a.engine.RecordRelatedConversationRef(ctx, string(interaction.System),
		"[the reply above is incomplete — "+result.Interrupted+"]", ref.ID, interaction.Details{ReasonCode: "interrupted"}); err != nil {
		logsink.Warn("turn.error", "interruption marker not recorded: %v", err)
	}
}
