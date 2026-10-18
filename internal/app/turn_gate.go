package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/interaction"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
)

func (a *App) acquireTurn(ctx context.Context) error {
	_, err := a.acquireTurnOr(ctx, nil)
	return err
}

func (a *App) acquireTurnOr(ctx context.Context, wake <-chan struct{}) (bool, error) {
	if a.turnGate == nil {
		return false, errors.New("application turn gate is not initialized")
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	select {
	case <-ctx.Done():
		return false, ctx.Err()
	case <-wake:
		return false, nil
	case <-a.turnGate:

		a.turnTaken()
		if err := ctx.Err(); err != nil {
			a.releaseTurn()
			return false, err
		}
		return true, nil
	}
}

func (a *App) turnTaken() {
	rel := a.fg.Acquire("turn")
	a.turnMu.Lock()
	if a.turnFgRelease != nil {
		a.turnFgRelease()
	}
	a.turnFgRelease = rel
	a.turnMu.Unlock()
	a.emitPluginEvent(pluginhost.TopicTurnStarted, nil)
}

func (a *App) releaseTurn() {
	a.emitPluginEvent(pluginhost.TopicTurnEnded, nil)

	a.turnMu.Lock()
	a.turnFacility = false
	fgRel := a.turnFgRelease
	a.turnFgRelease = nil

	unanswered := a.turnVoice
	a.turnVoice = nil
	a.turnMu.Unlock()
	if fgRel != nil {
		fgRel()
	}
	for _, b := range unanswered {
		b.release("the turn ended without answering")
		a.retirePendingObservation(b)
	}

	a.turnMu.Lock()
	leftovers := a.steers
	a.steers = nil
	kept := len(leftovers) > 0 && a.steerFlush == nil
	if !kept {
		a.turnGate <- struct{}{}
	}
	a.turnMu.Unlock()
	if a.turnAfterHandoff != nil {
		a.turnAfterHandoff()
	}

	a.pokeOutbox()

	if len(leftovers) == 0 {
		return
	}
	if !kept {
		releaseVoice(leftovers, "flushed by the test hook")
		a.steerFlush(leftovers)
		return
	}
	if !a.runBackground(func() { a.runLeftoverSteerTurn(leftovers) }) {

		a.turnMu.Lock()
		leftovers = append(leftovers, a.steers...)
		a.steers = nil
		a.turnGate <- struct{}{}
		a.turnMu.Unlock()
		a.refuseSteers(leftovers, "runtime stopped before the queued words could run")
	}
}

func (a *App) refuseSteers(entries []steerEntry, why string) {
	releaseVoice(entries, why)
	for _, e := range entries {
		a.retirePendingObservation(e.voice)
		dashboard.ChatNotRecorded(e.recordCtx, why)
	}
}

func (a *App) runLeftoverSteerTurn(entries []steerEntry) {
	a.turnTaken()
	defer a.releaseTurn()

	if a.conv == nil || a.engine == nil || a.store == nil {
		logsink.Warn("steering.refusal", "%d leftover message(s) arrived before the runtime could run turns — dropped", len(entries))
		a.refuseSteers(entries, "dropped: the runtime could not run turns")
		return
	}

	ctx := a.lifetime()
	if err := ctx.Err(); err != nil {

		logsink.Warn("steering.error", "%d leftover message(s) could not open their turn: %v", len(entries), err)
		a.refuseSteers(entries, "the turn could not open: "+err.Error())
		return
	}
	ctx, done := a.beginCancellableTurn(ctx)
	defer done()

	listen, _, _ := a.voiceMode()
	entries, room := splitRoom(entries, listen == listenMeeting)
	a.recordRoom(room)
	kept := entries[:0:0]
	for _, e := range entries {
		if !a.arrivalAlreadyRecorded(e.recordCtx) {
			kept = append(kept, e)
		}
	}
	entries = kept
	if len(entries) == 0 {
		if len(room) > 0 && a.dashboard != nil {
			a.dashboard.BroadcastSteering()
		}
		return
	}
	parts := make([]string, 0, len(entries))

	refusals := make([]error, len(entries))
	failed := 0
	for i, e := range entries {

		ref, err := a.recordConversationForReceipt(ctx, e.recordCtx, e.role, e.content)
		if err != nil {
			logsink.Warn("steering.error", "a queued message was not recorded: %v", err)
			refusals[i] = err
			failed++
			continue
		}

		a.annotateVoiceTurn(ref.Sequence, e.voice)

		if e.recordCtx != nil {
			if err := a.recordCameWith(interaction.WithTurn(context.WithoutCancel(e.recordCtx), interaction.TurnID(ctx)), ref.ID); err != nil {
				logsink.Warn("steering.error", "what came with a queued message was not recorded: %v", err)
				refusals[i] = err
				failed++
				continue
			}
		}
		parts = append(parts, e.content)
	}
	if failed > 0 {

		a.leftoverNotRun(entries, refusals)
		return
	}
	logsink.Info("steering.decision", "%d leftover message(s) opened their own turn", len(entries))
	if operatorSpoke(entries) {
		a.resetAsk()
	}
	for _, e := range entries {
		a.holdVoice(e.voice)
	}
	resp, err := a.runTurnLocked(withTurnSource(ctx, turnSourceSteer), strings.Join(parts, "\n\n"))
	if err != nil {
		logsink.Warn("steering.error", "leftover turn failed: %v", err)
		if a.dashboard != nil {
			why := "Your queued message could not run: "
			if resp != "" {

				if !a.voiceReplyShown.Swap(false) {
					a.dashboard.BroadcastResponse("identity", resp)
				}
				why = "Your queued message was answered, but "
			}
			a.dashboard.BroadcastResponse("system", why+err.Error())
		}
		return
	}
	if a.dashboard != nil && !a.voiceReplyShown.Swap(false) {
		a.dashboard.BroadcastResponse("identity", resp)
	}
}

func (a *App) leftoverNotRun(entries []steerEntry, refusals []error) {
	var first error
	failed := 0
	for _, err := range refusals {
		if err == nil {
			continue
		}
		if first == nil {
			first = err
		}
		failed++
	}
	logsink.Warn("steering.refusal", "%d of %d leftover message(s) were not recorded; their turn was not run", failed, len(entries))
	releaseVoice(entries, "their turn was not run: a queued message was not recorded: "+first.Error())

	type heard struct {
		spoken, refused int
		why             error
	}
	sessions := map[string]*heard{}
	var order []string
	for i, e := range entries {
		if e.voice == nil {
			continue
		}
		h := sessions[e.voice.session]
		if h == nil {
			h = &heard{}
			sessions[e.voice.session] = h
			order = append(order, e.voice.session)
		}
		h.spoken++
		if refusals[i] != nil {
			h.refused++
			if h.why == nil {
				h.why = refusals[i]
			}
			a.retirePendingObservation(e.voice)
		}
	}
	for _, session := range order {
		switch h := sessions[session]; {
		case h.refused == 0:
			a.noteReplyOutcome(session, "no reply: the words are recorded; their turn was not run because another queued message was not recorded: "+first.Error())
		case h.spoken == 1:
			a.noteReplyOutcome(session, "no reply: the words were not recorded: "+h.why.Error())
		default:
			a.noteReplyOutcome(session, fmt.Sprintf("no reply: %d of %d utterances were not recorded: %v", h.refused, h.spoken, h.why))
		}
	}
	if a.dashboard == nil {
		return
	}
	a.dashboard.BroadcastSteering()
	var said string
	switch {
	case len(entries) == 1:
		said = "A queued message was not recorded, so it was not run: " + first.Error()
	case failed == len(entries):
		said = fmt.Sprintf("%d queued messages were not recorded, so they were not run: %v", failed, first)
	default:
		said = fmt.Sprintf("%d of %d queued messages were not recorded, so none was run: %v. The others are recorded and will be read with the next message.", failed, len(entries), first)
	}
	a.dashboard.BroadcastResponse("system", said)
}

func (a *App) pokeOutbox() {
	select {
	case a.outboxPoke <- struct{}{}:
	default:
	}
}
