package app

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/aiii-dot-id/aii-os/internal/conversation"
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
func replayContent(role, content string) string {
	if role == "system" {
		return replayView(content)
	}
	return content
}

// .
// .
// .
// .
func replayView(content string) string {
	// .
	// .
	// .
	// .
	// .
	// .
	// .
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

// .
// .
// .
// .
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
		// .
		// .
		// .
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

// .
// .
func (a *App) handleMessageLocked(ctx context.Context, msg string) (string, error) {
	seq, err := a.engine.RecordConversationTurnSeq("operator", msg)
	if err != nil {
		return "", fmt.Errorf("record operator turn: %w", err)
	}
	a.tagSpokenTurn(seq, msg)
	return a.runTurnLocked(ctx, msg)
}

// .
// .
// .
// .
func (a *App) runTurnLocked(ctx context.Context, msg string) (string, error) {
	reply, err := a.runTurnLockedInner(ctx, msg)
	// .
	// .
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
	lastResident, err := a.store.LastTurnAtMs("resident")
	if err != nil {
		return "", fmt.Errorf("load last resident turn: %w", err)
	}
	if firings, err := a.store.TimerFiringsSince(lastResident); err != nil {
		return "", fmt.Errorf("load timer firings: %w", err)
	} else if len(firings) > 0 {
		var lines []string
		for _, f := range firings {
			lines = append(lines, "- "+f.Content)
		}
		workState = strings.TrimSpace(workState + "\n\n## Your timers fired since your last turn\n" +
			strings.Join(lines, "\n") +
			"\n(These are facts delivered by the time system.)")
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
	if arrivals, err := a.store.InboundSince(lastResident); err != nil {
		return "", fmt.Errorf("load arrivals: %w", err)
	} else if len(arrivals) > 0 {
		var lines []string
		for _, m := range arrivals {
			// .
			// .
			// .
			lines = append(lines, "- "+a.frameStoredArrival(m))
		}
		workState = strings.TrimSpace(workState + "\n\n## Messages that arrived since your last turn\n" +
			strings.Join(lines, "\n") +
			"\n(Answer any that deserve it with send, or leave them.)")
	}
	// .
	// .
	// .
	if outcomes, err := a.store.OutboxOutcomesSince(lastResident); err != nil {
		return "", fmt.Errorf("load delivery outcomes: %w", err)
	} else if len(outcomes) > 0 {
		workState = strings.TrimSpace(workState + "\n\n## What became of the messages you sent\n" +
			deliveryOutcomeLines(outcomes) +
			"\n(Delivered: an adapter took it. Response lost: it may have arrived — do not resend blindly. Parked: no more attempts; your operator can see it.)")
	}
	reserve, err := a.promptReserve(current, omitted+len(conv)-1)
	if err != nil {
		return "", err
	}
	p, err := a.composer.Compose(workState, reserve)
	if err != nil {
		return "", fmt.Errorf("prompt compose: %w", err)
	}

	// .
	// .
	// .
	// .
	// .
	result, err := a.conv.RunSystem(ctx, a.gatedSystem(p), conv, omitted)
	if err != nil {
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
		a.recordInterruptedTurn(result)
		return "", err
	}
	a.recordTurnCost(result.Usage)
	// .
	// .
	a.markComposedHarvests()
	a.noteYield(result.Yielded)
	a.noteTurnShape(result.ContinuedAtCap || result.ContinuedAtPressure, result.ContinuedAtPressure)
	finalText := result.FinalText

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
	if result.Spoken != "" {
		if err := a.engine.RecordConversationTurn("resident", result.Spoken); err != nil {
			return "", fmt.Errorf("record resident turn: %w", err)
		}
		return result.Spoken, nil
	}

	return finalText, nil
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
func (a *App) recordInterruptedTurn(result conversation.Result) {
	a.recordTurnCost(result.Usage)
	if result.Spoken == "" {
		return
	}
	if err := a.engine.RecordConversationTurn("resident", result.Spoken); err != nil {
		log.Printf("Warning: interrupted turn not recorded: %v", err)
		return
	}
	if err := a.engine.RecordConversationTurn("system",
		"[the reply above is incomplete — "+result.Interrupted+"]"); err != nil {
		log.Printf("Warning: interruption marker not recorded: %v", err)
	}
}
