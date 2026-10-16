package app

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/interaction"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"strings"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/conversation"
	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/store"
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
	if err := a.recordCameWith(ctx, ref.ID); err != nil {

		return "", fmt.Errorf("the message was recorded, but %w; it was not answered. Send it again", err)
	}
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

	if options, constrained := conversation.RequestOptionsFromContext(ctx); constrained && options.ResponseFormat != nil {

		spoken := result.Spoken
		if spoken == "" {
			spoken = result.FinalText
		}
		reply, err := a.answer(ctx, spoken, result)
		if err != nil {
			return reply, err
		}
		if result.ContractErr != nil {
			return reply, result.ContractErr
		}
		dashboard.ChatStructuredOutput(ctx, json.RawMessage(result.FinalText))
		return reply, nil
	}
	if result.Spoken != "" {
		return a.answer(ctx, result.Spoken, result)
	}
	if finalText != "" {
		return a.answer(ctx, finalText, result)
	}
	return finalText, nil
}

func (a *App) recordInterruptedTurnContext(ctx context.Context, result conversation.Result) bool {
	kept := true
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	a.recordTurnCost(ctx, result)
	about := ""
	if result.Spoken != "" {
		ref, err := a.recordReply(ctx, result.Spoken, result)
		if err != nil {
			kept = false
			logsink.Warn("turn.error", "interrupted turn not recorded: %v", err)
		} else {
			about = ref.ID
			if _, err := a.engine.RecordRelatedConversationRef(ctx, string(interaction.System),
				"[the reply above is incomplete — "+result.Interrupted+"]", ref.ID, interaction.Details{ReasonCode: "interrupted"}); err != nil {
				kept = false
				logsink.Warn("turn.error", "interruption marker not recorded: %v", err)
			}
		}
	}
	return a.recordUnanswered(ctx, result.Unanswered, result.Interrupted, about) && kept
}

func (a *App) recordUnanswered(ctx context.Context, words []string, cause, about string) bool {
	if len(words) == 0 {
		return true
	}
	if about == "" {
		about = a.unansweredAnchor(ctx)
	}
	if about == "" {

		logsink.Error("turn.error", "%d unanswered mid-turn message(s) have no words to stand beside: their note is recorded but will not reach the next turn", len(words))
	}
	if _, err := a.engine.RecordRelatedConversationRef(ctx, string(interaction.System), unansweredNote(words, cause), about,
		interaction.Details{ReasonCode: store.ReasonSteeringUnanswered}); err != nil {
		logsink.Warn("turn.error", "the note on %d unanswered mid-turn message(s) was not recorded: %v", len(words), err)
		return false
	}
	return about != ""
}

func unansweredNote(words []string, cause string) string {
	quoted := make([]string, len(words))
	for i, w := range words {
		quoted[i] = "«" + w + "»"
	}
	ended := "it ended before answering"
	if cause != "" {
		ended += " (" + cause + ")"
	}
	return "[said while that turn was working, and " + ended + ": " + strings.Join(quoted, " ") + "]"
}

func (a *App) unansweredAnchor(ctx context.Context) string {
	steps := []string{""}
	if turn := interaction.TurnID(ctx); turn != "" {
		steps = []string{turn, ""}
	}
	safe := a.engine.SafeTranscript()
	for _, of := range steps {
		for i := len(safe) - 1; i >= 0; i-- {
			r := safe[i].Record
			if (of == "" || r.TurnID == of) && r.Role != interaction.System && r.Kind == interaction.Message {
				return r.ID
			}
		}
		if a.store == nil {
			continue
		}
		id, err := a.store.LatestWords(of)
		if err != nil {
			logsink.Warn("turn.error", "the words a failed turn's note stands beside could not be read: %v", err)
		}
		if id != "" {
			return id
		}
	}
	return ""
}
