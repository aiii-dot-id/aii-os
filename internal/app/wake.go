package app

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/aiii-dot-id/aii-os/internal/llm"
)

// wake.go — the identity waking to something that was not said to it.
//
// A timer it set goes off. A message arrives. Neither is an operator
// turn, and both need the same seven steps: record the fact, take the
// gate, build the prompt, think, speak. This is those steps, once.
//
// It was twice. The timer path had its own copy and inbound was about to
// add a third, which is how three slightly different turns end up in one
// process — the timer's copy had quietly drifted, missing both the cancel
// scope and the cost accounting that operator turns get.
func (a *App) wake(ctx context.Context, role, fact string) (string, error) {
	// THE CALLER HOLDS THE GATE AND THE CALLER RELEASES IT — one
	// identity, one voice, and how the gate was obtained is the caller's
	// business: a timer waits for it, an arriving message takes it
	// through AdmitParticipant or steers instead.
	//
	// This function used to release what it never took. That asymmetry
	// WAS a bug: the nil check below returned before the defer was
	// armed, so a wake attempted while the runtime was not fully live
	// LEAKED THE GATE. TurnActive stays true forever after that — every
	// message steers into a turn that does not exist, no new turn can
	// start, metabolism defers forever, and only a restart recovers it.
	// Whoever takes it releases it; there is now no path where those are
	// different functions.
	if a.conv == nil || a.composer == nil || a.engine == nil {
		return "", fmt.Errorf("the identity is not live")
	}
	ctx, cancel := a.beginCancellableTurn(ctx)
	defer cancel()

	// Recorded before the thinking, so a turn that fails still leaves the
	// identity knowing what happened.
	//
	// THE ROLE IS THE CALLER'S because the callers differ in kind. A
	// timer firing is a substrate FACT — "system", which buildHistory
	// skips and the working state re-supplies from TimerFiringsSince. An
	// arriving message is a PERSON, and recording that as "system" meant
	// it vanished from history: the next turn showed the identity's
	// reply with nothing it was replying to. Neither is ever "operator".
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
		return "", err
	}
	a.recordTurnCost(result.Usage)

	spoken := result.Spoken
	if spoken == "" {
		spoken = result.FinalText
	}
	if strings.TrimSpace(spoken) == "" {
		// Silence is a legitimate answer to a message and to an alarm.
		return "", nil
	}
	if err := a.engine.RecordConversationTurn("resident", spoken); err != nil {
		log.Printf("FAILED to record wake speech: %v — the transcript is missing what was said", err)
	}
	return spoken, nil
}
