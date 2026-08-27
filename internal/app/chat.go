package app

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/untrusted"
)

// replayContent bounds a replayed turn ONLY when the substrate authored
// it. What a person or an identity SAID replays verbatim: bounding it
// spliced substrate text — "…[trimmed for replay]…" — into the middle of
// someone's words, and since the founding greeting became a transcript
// turn, the founding record passes through here (review 2026-08-20).
// Tool rows (role "system") are the substrate editing its own text,
// where the marker is honest. Volume stays the operator's knob:
// prompt.recent_turns.
func replayContent(role, content string) string {
	if role == "system" {
		return replayView(content)
	}
	return content
}

// replayView bounds one transcript turn for the connect replay. Tool
// events keep the call (summary) plus a head+tail excerpt of the result;
// chat turns are passed through (they are already the model's own words,
// bounded by output limits).
func replayView(content string) string {
	// Bug hunt 2026-08-18 (#1): the old body sliced content[nl+3:] (the
	// arrow marker "\n← " is FIVE bytes — ← is 3-byte UTF-8, so every
	// truncated replay carried a torn rune) and then result[:600] /
	// result[len(result)-300:] without bounds checks — one stored tool
	// event with a long arg and a short result panicked the WS handler,
	// and the SPA'''s 2s reconnect replayed the same row forever: a
	// poisoned row bricked the dashboard. Everything is clamped now.
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

// observeChat runs one operator turn with THE GATE ALREADY HELD by its
// caller — AdmitChat took it, atomically, at the moment it decided this
// message was not a steer. Acquiring here as well would reopen the
// window it exists to close. Releasing stays here, with the turn.
func (a *App) observeChat(ctx context.Context, msg string, emit func(kind, name, args string)) (string, error) {
	defer a.releaseTurn()
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
	return a.handleMessageLocked(ctx, msg)
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

// handleMessageLocked is the core conversation loop: record → compose →
// LLM → tool loop → respond. Caller owns the turn token.
func (a *App) handleMessageLocked(ctx context.Context, msg string) (string, error) {
	if err := a.engine.RecordConversationTurn("operator", msg); err != nil {
		return "", fmt.Errorf("record operator turn: %w", err)
	}
	return a.runTurnLocked(ctx, msg)
}

// runTurnLocked is the turn AFTER the arrival is recorded: compose →
// LLM → tool loop → respond. Split from handleMessageLocked so the
// leftover-steer flush can run a turn whose arrivals were recorded
// entry by entry, under the roles they arrived with.
func (a *App) runTurnLocked(ctx context.Context, msg string) (string, error) {
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
	// Messages that arrived while nobody was talking to the identity.
	//
	// An arrival from someone granted a wake starts its own turn; one
	// from anyone else was recorded and read by NOBODY, while the
	// comment above that path claimed "the next turn carries it". This
	// is the turn, and this is it carrying them.
	//
	// Same question the timer block above asks, against the same clock:
	// what happened since I last spoke. No seen flag to maintain, and
	// nothing to forget to update.
	if arrivals, err := a.store.InboundSince(lastResident); err != nil {
		return "", fmt.Errorf("load arrivals: %w", err)
	} else if len(arrivals) > 0 {
		var lines []string
		for _, m := range arrivals {
			who, _ := a.whoIs(m.Channel, m.Address)
			if who == "" {
				who = m.Address // a stranger is named by their address
			}
			// WRAPPED, like every other arrival: foreign text reaching a
			// prompt through working state is still foreign text.
			lines = append(lines, "- "+who+" on "+m.Channel+":\n"+
				untrusted.Wrap(m.Channel+":"+m.Address, m.Body))
		}
		workState = strings.TrimSpace(workState + "\n\n## Messages that arrived since your last turn\n" +
			strings.Join(lines, "\n") +
			"\n(Answer any that deserve it with send, or leave them.)")
	}
	reserve, err := a.promptReserve(current, omitted+len(conv)-1)
	if err != nil {
		return "", err
	}
	p, err := a.composer.Compose(workState, reserve)
	if err != nil {
		return "", fmt.Errorf("prompt compose: %w", err)
	}

	// The conversation loop: compose → LLM → tool calls → speak. The
	// loop owns spoken accumulation, the context guard, truncation
	// banners, and the transcript record (tested in internal/conversation).
	// THE GATE: the composed prompt passes the ring contract before it
	// reaches the model — verbatim 0/5/1, whole Ring 2, elastic 3/4.
	result, err := a.conv.RunSystem(ctx, a.gatedSystem(p), conv, omitted)
	if err != nil {
		return "", err
	}
	a.recordTurnCost(result.Usage)
	finalText := result.FinalText

	// Parse verb directives from text (identity verbs like note, recall)
	resp := &llm.Response{Choices: []llm.Choice{{Message: llm.Message{Content: finalText}}}}
	actions, _ := llm.ParseResponse(resp)
	actionCtx := llm.WithModelID(ctx, result.ModelID)
	for _, action := range actions {
		if action.Type == "verb" {
			result, err := a.engine.ExecuteAction(actionCtx, action.Type, action.Name, action.Args)
			if err != nil {
				log.Printf("verb %s error: %v", action.Name, err)
			} else if result != "" {
				log.Printf("verb %s: %s", action.Name, result)
			}
		}
	}

	// Everything the model said aloud across the loop is the reply — the
	// operator sees the full utterance, not just the post-tool tail.
	if result.Spoken != "" {
		if err := a.engine.RecordConversationTurn("resident", result.Spoken); err != nil {
			return "", fmt.Errorf("record resident turn: %w", err)
		}
		return result.Spoken, nil
	}

	return finalText, nil
}
