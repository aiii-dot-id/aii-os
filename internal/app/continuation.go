package app

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/conversation"
	"github.com/aiii-dot-id/aii-os/internal/interaction"
	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

type capReason int

const (
	noCap capReason = iota
	capDeclared
	capContext
	capTokenFence
	capRounds
	capDeadline
	capTimeFence
	capRestart
	capEnded
	capTick
)

const timeFenceHow = "because it spent its time"

func capReasonOf(r conversation.Result) capReason {
	switch {
	case r.ContinuedAtTokenBudget:
		return capTokenFence
	case r.ContinuedAtTimeFence:
		return capTimeFence
	case r.ContinuedAtPressure:
		return capContext
	case r.ContinuedAtCap:
		return capDeclared
	case r.ContinuedAtRounds || r.ExhaustedBudget:

		return capRounds
	}
	return noCap
}

func (r capReason) how() string {
	switch r {
	case capDeadline:
		return "because it ran out of time"
	case capContext:
		return "because its context filled"
	case capTokenFence:
		return "because it spent its token budget"
	case capTimeFence:
		return timeFenceHow
	case capRounds:
		return "at its round limit"
	case capEnded:
		return "by itself"
	case capRestart:
		return "at a checkpoint, and the identity was restarted before this fresh turn ran"
	case capTick:

		return "with nothing holding the work, so the host started this one"
	}
	return "at its declared tool budget"
}

type turnEnding struct {
	kind endingKind
	why  string
}

type endingKind int

const (
	endedByItself endingKind = iota
	continued
	workDelivered
	nothingFollows
)

func (e turnEnding) closing(r conversation.Result) []string {
	switch e.kind {
	case workDelivered:
		return r.Delivered()
	case nothingFollows:
		return r.NothingContinues(e.why)
	}
	return r.Notes
}

func (a *App) noteTurnShape(reason capReason, from string, yielded bool) turnEnding {

	ending := a.scheduleContinuation(reason, from, yielded)
	a.settleMetConditions(from)
	a.addQueuedLegs(0)
	if ending.kind != continued {
		a.sweepDeliveriesAfterTurn()
	}
	return ending
}

func (a *App) sweepDeliveriesAfterTurn() {
	if a.store == nil {
		return
	}

	if hw := a.configSnapshot().Agency.HarvestWake; hw != nil && !*hw {
		return
	}

	unh, err := a.store.UnharvestedDeliveries(8)
	if err != nil {
		return
	}
	i := slices.IndexFunc(unh, func(w store.WorkSession) bool { return a.outcomeTooLarge[w.ID] < 2 })
	if i < 0 {
		return
	}
	id, goal := unh[i].ID, store.SubagentGoal(unh[i].Description)

	logsink.Info("harvest.decision", "delivery unharvested at turn end (%s) — waking to harvest", id)

	gateCtx, cancelGate := context.WithTimeout(a.bgCtx, harvestGateWait)
	if !a.runBackground(func() {
		defer cancelGate()
		a.wakeSubagentDelivery(gateCtx, id, goal)
	}) {
		cancelGate()
	}
}

func (a *App) leftToNow(from string) bool {
	ws, err := a.sessionOfTurn(from)
	return err == nil && ws != nil && ws.ResumesOn == "now"
}

func (a *App) scheduleContinuation(reason capReason, from string, yielded bool) turnEnding {
	if reason == noCap {

		if yielded && !a.leftToNow(from) {
			a.turn.mu.Lock()
			a.turn.contChain = 0
			a.turn.mu.Unlock()
			return turnEnding{kind: endedByItself}
		}
		reason = capEnded
	}
	cfg := a.configSnapshot()
	if !agencyOn(cfg.Agency.TurnContinuation) {
		return turnEnding{nothingFollows, "continuation is switched off (agency.turn_continuation)"}
	}
	bound := a.residentLimitsFor(cfg).Value(continuationsSetting)

	ws, err := a.sessionOfTurn(from)
	if err != nil {
		logsink.Warn("continuation.error", "not scheduled: the active work session could not be read: %v", err)
		return turnEnding{nothingFollows, "the host could not read the work session"}
	}
	if ws == nil && reason != capDeadline {
		delivered, err := a.store.WorkDeliveredInTurn(from)
		if err != nil {
			logsink.Warn("continuation.error", "whether this turn delivered its work could not be read: %v", err)
		}
		if reason == capEnded {

			a.turn.mu.Lock()
			a.turn.contChain = 0
			a.turn.mu.Unlock()
		}
		if delivered {
			return turnEnding{kind: workDelivered}
		}
		if reason == capEnded {
			return turnEnding{kind: endedByItself}
		}
		return turnEnding{nothingFollows, "no work session is active"}
	}
	sessionID := ""
	if ws != nil {
		sessionID = ws.ID
	}

	if from != "" {
		source, _, err := a.store.TurnSourceOf(from)
		if err != nil {
			logsink.Warn("continuation.error", "not scheduled: what opened turn %s could not be read: %v", from, err)
			return turnEnding{nothingFollows, "the host could not read what opened the turn"}
		}
		if source == turnSourceArrival {
			return turnEnding{nothingFollows, "a turn an arrival opened is not continued unattended"}
		}
	}

	if ws != nil {
		if ws.ResumesOn != "" && ws.ResumesOn != "now" {
			a.resetChain()
			if reason == capEnded {
				return turnEnding{kind: endedByItself}
			}
			on, _ := a.conditionWords(ws)
			return turnEnding{nothingFollows, "this work waits for " + on + ", as you set"}
		}
		ending := a.continueWork(ws, reason, from)
		if ending.kind != continued {
			a.resetChain()
		}
		return ending
	}
	a.turn.mu.Lock()
	a.turn.contChain++
	leg := a.turn.contChain
	a.turn.mu.Unlock()
	if !bound.Allows(leg - 1) {
		logsink.Warn("continuation.refusal", "refused: %d consecutive capped turns on %s, and agency.max_turn_continuations is %s — standing down so the operator can look", leg-1, sessionID, bound)
		return turnEnding{nothingFollows, chainSpentWhy(bound.N)}
	}
	a.addQueuedLegs(1)
	if !a.runBackground(func() { a.continueCappedTurn(sessionID, leg, reason, from, false, "") }) {
		a.addQueuedLegs(-1)
		logsink.Info("continuation.refusal", "not scheduled for %s: runtime stopping", sessionID)
		return turnEnding{nothingFollows, "the identity is stopping"}
	}
	return turnEnding{kind: continued}
}

func (a *App) resetChain() {
	a.turn.mu.Lock()
	a.turn.contChain = 0
	a.turn.mu.Unlock()
}

func (a *App) addQueuedLegs(n int) {
	a.turn.mu.Lock()
	a.turn.legsQueued += n
	a.turn.mu.Unlock()
	if a.dashboard != nil {
		a.dashboard.PokeWork()
	}
}

func (a *App) resumeTimedOutTurn(ctx context.Context, err error, recorded bool) {
	if !recorded || a.lifetime().Err() != nil || errors.Is(ctx.Err(), context.Canceled) || errors.Is(err, context.Canceled) {
		return
	}
	if !errors.Is(err, context.DeadlineExceeded) && !errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return
	}

	if _, constrained := conversation.RequestOptionsFromContext(ctx); constrained {
		return
	}
	switch interaction.TurnSource(ctx) {
	case turnSourceOperator, turnSourceSteer, turnSourceVoice, turnSourceHarvest, turnSourceContinuation:
	default:
		return
	}
	ending := a.scheduleContinuation(capDeadline, interaction.TurnID(ctx), false)
	message := "The turn ran out of time. Its unfinished request is scheduled to resume in a fresh turn from the recorded state; completed tool calls are not automatically replayed."
	if ending.kind != continued {
		message = "The turn ran out of time. Nothing continues by itself: " + ending.why + ". Ask again to continue."
	}
	id := fmt.Sprintf("continuation_timeout_%d", time.Now().UTC().UnixNano())
	if e := a.store.AddOutboxNotice(id, "operator", "", message, nil); e != nil {
		logsink.Warn("continuation.error", "timeout disposition could not be recorded: %v", e)
	}
}

func chainSpentWhy(bound int) string {
	turns := "fresh turns"
	if bound == 1 {
		turns = "fresh turn"
	}
	return fmt.Sprintf("the host has already started %d %s in a row, its limit (agency.max_turn_continuations)", bound, turns)
}

type legMeter struct {
	mu        sync.Mutex
	predicted int
	ordinal   int
}

func (m *legMeter) get() (remaining, ordinal int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.predicted, m.ordinal
}

func (a *App) registerLegMeter(sessionID string) (*legMeter, func()) {
	m := &legMeter{}
	a.turn.mu.Lock()
	if a.turn.legMeters == nil {
		a.turn.legMeters = map[string]*legMeter{}
	}
	a.turn.legMeters[sessionID] = m
	a.turn.mu.Unlock()
	return m, func() {
		a.turn.mu.Lock()
		delete(a.turn.legMeters, sessionID)
		a.turn.mu.Unlock()
	}
}

func (a *App) noteSubagentWorkCall(sessionID string, args map[string]any, ordinal int) {
	_, predicted, _ := workDeclaration(args)
	if predicted <= 0 {
		return
	}
	a.turn.mu.Lock()
	m := a.turn.legMeters[sessionID]
	a.turn.mu.Unlock()
	if m == nil {
		return
	}
	m.mu.Lock()
	if ordinal >= m.ordinal {
		m.predicted, m.ordinal = predicted, ordinal
	}
	m.mu.Unlock()
}

func (a *App) resetAsk() {
	a.turn.mu.Lock()
	a.turn.askYields, a.turn.askFleetSpent, a.turn.contChain = 0, false, 0
	a.turn.mu.Unlock()
	a.wordCame()
}

func (a *App) noteYield(yielded bool) {
	if !yielded {
		return
	}
	a.turn.mu.Lock()
	a.turn.askYields++
	a.turn.mu.Unlock()
}

func (a *App) markFleetSpent() {
	a.turn.mu.Lock()
	a.turn.askFleetSpent = true
	a.turn.mu.Unlock()
}

func (a *App) yieldGate() (bool, string) {
	if !agencyOn(a.configSnapshot().Agency.YieldAnswer) {
		return false, ""
	}
	a.turn.mu.Lock()
	defer a.turn.mu.Unlock()
	if a.turn.askFleetSpent {
		return true, "a sub-agent has ended unfinished or failed since the operator last spoke, and the operator has no answer yet"
	}
	if a.turn.askYields >= 2 {
		return true, fmt.Sprintf("this would be yield %d against the operator's open ask with no answer yet", a.turn.askYields+1)
	}
	return false, ""
}

func (a *App) continueCappedTurn(sessionID string, leg int, reason capReason, from string, underNow bool, promise string) {
	if err := a.acquireTurn(a.bgCtx); err != nil {
		logsink.Warn("continuation.error", "%s: could not take the turn: %v", sessionID, err)
		a.addQueuedLegs(-1)
		a.legDidNotRun(sessionID, leg, from, err)
		return
	}
	defer a.releaseTurn()

	a.addQueuedLegs(-1)

	if !agencyOn(a.configSnapshot().Agency.TurnContinuation) {
		a.legStandsDown(sessionID, leg, from, "continuation was switched off (agency.turn_continuation) before it began")
		return
	}
	if sessionID != "" {
		ws, err := a.store.WorkSessionByID(sessionID)
		switch {
		case err != nil:
			logsink.Warn("continuation.error", "%s: the session could not be read before leg %d began: %v", sessionID, leg, err)
			a.legStandsDown(sessionID, leg, from, "the host could not read its work session before it began")
			return
		case ws != nil && ws.Status != "active":
			logsink.Info("continuation.refusal", "%s: leg %d stands down — the session was %s before it began", sessionID, leg, ws.Status)
			a.legStoodDown(sessionID, leg, from)
			return
		}
	}
	selected, err := a.store.ActiveWorkSession()

	if err == nil && underNow && sessionID != "" && (selected == nil || selected.ID != sessionID) {
		if ws, werr := a.store.WorkSessionByID(sessionID); werr == nil && ws != nil && ws.Status == "active" {
			selected = ws
		}
	}
	if err != nil || (selected == nil && sessionID != "") || (selected != nil && selected.ID != sessionID) {
		if reason == capDeadline {
			a.legDidNotRun(sessionID, leg, from, fmt.Errorf("the work selection changed or could not be read; the timed-out request was not resumed"))
			return
		}
		if err != nil {
			logsink.Warn("continuation.error", "%s: the active work session could not be read before leg %d began: %v", sessionID, leg, err)
			a.legStandsDown(sessionID, leg, from, "the host could not read its work session before it began")
			return
		}
		a.legStandsDown(sessionID, leg, from, "the active work changed before it began")
		return
	}

	if underNow {
		switch {
		case selected == nil || selected.ID != sessionID || selected.Promise != promise:
			a.legStandsDown(sessionID, leg, from, "the promise it was queued under is no longer the session's")
			return
		case selected.ResumesOn != "now":
			a.legStandsDown(sessionID, leg, from, "what resumes this work changed before it began")
			return
		case selected.Promise != "" && !a.promisedUnattended(selected):

			a.setResumesOn(selected.ID, from, "word", "The turn that made this promise was an arrival's, or is not in the record; the work resumes on the operator's word")
			a.legStandsDown(sessionID, leg, from, "the turn that made its promise was an arrival's")
			return
		}
		held, err := a.heldByOperator()
		if err != nil {

			logsink.Warn("continuation.error", "%s: the operator's stage could not be read before leg %d began: %v", sessionID, leg, err)
			a.legStandsDown(sessionID, leg, from, "the host could not read the operator's stage before it began; it is tried again at the next tick")
			return
		}
		if held {
			a.holdPromise(selected, from, "a fresh turn was to carry this work on")
			a.legStandsDown(sessionID, leg, from, "the operator holds this work")
			return
		}
		if past, at := a.pastCeiling(); past {
			a.waitForTomorrow(selected, from, at)
			a.legStandsDown(sessionID, leg, from, "today's ceiling is reached")
			return
		}
	}

	turnCtx, cancelTurn := context.WithTimeout(withTurnSource(a.lifetime(), turnSourceContinuation), a.turnWall(continuationWallSetting))
	defer cancelTurn()
	if sessionID != "" {
		turnCtx = interaction.WithResumeSession(turnCtx, sessionID)
	}

	ended := fmt.Sprintf("Your previous turn ended %s.", reason.how())
	if from != "" {
		if latest, err := a.store.LatestDialogueTurn(); err != nil {
			logsink.Warn("continuation.error", "%s: the newest turn could not be read before leg %d: %v", sessionID, leg, err)
		} else if latest != "" && latest != from {
			ended = fmt.Sprintf("A turn of yours ended %s, and another turn has run since.", reason.how())
		}
	}

	fact := fmt.Sprintf("[budget checkpoint — continuation %d] %s Work session %s is still active. Its plan= and next_move= may be older than that turn's closing answer: reconcile them with that answer and the recorded results before deciding what remains. Do not replay completed calls, or repeat an external action without checking its outcome. Then declare steps= for this leg.", leg, ended, sessionID)

	if reason == capEnded || reason == capTick {
		fact = fmt.Sprintf("[work continues — continuation %d] %s Work session %s is still active, so this fresh turn carries it on. Reconcile plan= and next_move= with that turn's closing answer and the recorded results, then do the next step. Do not replay completed calls, or repeat an external action without checking its outcome. Declare steps= for this leg.", leg, ended, sessionID)
	}
	if underNow {

		fact += " " + whatMovedSentence
		if _, nudge, nerr := a.store.SessionStuck(sessionID, store.DefaultStuckThresholds); nerr == nil && nudge != "" {
			fact += " The record says: " + nudge + "."
		}
	}
	if reason == capDeadline {
		fact = fmt.Sprintf("[timeout checkpoint — continuation %d] %s Resume the unfinished request from the recorded state. Inspect the recorded results before deciding what remains; completed tool calls are not to be replayed merely because the turn timed out. Do not assume an interrupted external action failed or repeat it without checking its outcome.", leg, ended)
		if sessionID != "" {
			fact += " Work session " + sessionID + " remains active."
		}
	}

	if selected != nil {
		if card := resumeCardFor(selected); card != "" {
			fact += "\n\n" + card
		}
	}

	unprompted := underNow
	if unprompted {
		fact += "\n\n" + a.unpromptedSentence()
	}

	under := ""
	if promise != "" {
		under = " under promise " + promise
	}
	logsink.InfoCtx(turnCtx, "continuation.start", "%s: leg %d begins%s; the turn before it ended %s", sessionID, leg, under, reason.how())
	spoken, recorded, err := a.wakeRecorded(turnCtx, string(interaction.System), fact)

	if spoken != "" {
		wakeID := fmt.Sprintf("wake_cont_%d", time.Now().UTC().UnixNano())
		var derr error
		if unprompted {
			derr = a.deliverUnprompted(turnCtx, wakeID, spoken, recorded)
		} else {
			derr = a.deliverReply(wakeID, spoken, recorded)
		}
		if derr != nil {
			logsink.Warn("continuation.error", "outbox write failed: %v", derr)
		}
	}
	if err != nil {
		logsink.Warn("continuation.error", "turn failed for %s (leg %d): %v", sessionID, leg, err)

		var turn *turnError
		started := errors.As(err, &turn) && turn.started
		switch {
		case spoken == "" && !started:
			a.legDidNotRun(sessionID, leg, from, err)
		case spoken == "" && turn.cutShort:

			a.legCutShort(sessionID, leg, err, errors.Is(turnCtx.Err(), context.DeadlineExceeded))
		}
		return
	}
	if spoken == "" {
		return
	}
	logsink.Info("continuation.end", "CONTINUATION: leg %d on %s spoke (%d chars)", leg, sessionID, len(spoken))
}

func (a *App) legDidNotRun(sessionID string, leg int, from string, cause error) {
	name, why, said := a.workName(sessionID), causeLine(cause), notStartedCause(cause)
	if errors.Is(cause, context.Canceled) {
		why = "it was stopped before it said anything"
		if a.lifetime().Err() != nil {
			why = "the identity was stopping"
		}
		said = why
	}
	a.tellNextPart(sessionID, leg, a.replyOf(from),
		fmt.Sprintf("The next part of %s did not run: %s.", name, why),
		fmt.Sprintf("[The next part of %s did not run: %s. Nothing continues it by itself.]", quotedWork(name), said))
}

func notStartedCause(cause error) string {
	if said := conversation.FailureCause(cause); said != conversation.SubstrateFailed {
		return said
	}
	var limit *llm.ContextLimitError
	var call *llm.CallError
	switch {
	case errors.As(cause, &limit):
		return fmt.Sprintf("its first request needed about %d input tokens, over the model's limit of %d, so it was not sent", limit.Required, limit.Limit)
	case errors.As(cause, &call):
		return "its first request to the model provider failed"
	}
	return "the host failed before the turn began"
}

func (a *App) legStoodDown(sessionID string, leg int, from string) {
	name := a.workName(sessionID)

	a.tellNextPart(sessionID, leg, a.store.LatestReply,
		fmt.Sprintf("The next part of %s did not run: that work was finished before it started.", name),
		fmt.Sprintf("[The next part of %s did not run: it was already finished.]", quotedWork(name)))
}

func (a *App) legStandsDown(sessionID string, leg int, from, why string) {
	logsink.Info("continuation.refusal", "%s: leg %d stands down — %s", sessionID, leg, why)
	name := a.workName(sessionID)
	a.tellNextPart(sessionID, leg, a.replyOf(from),
		fmt.Sprintf("The next part of %s did not run: %s.", name, why),
		fmt.Sprintf("[The next part of %s did not run: %s. Nothing continues it by itself.]", quotedWork(name), why))
}

func (a *App) legCutShort(sessionID string, leg int, cause error, wall bool) {
	why := causeLine(cause)
	var stop conversation.TurnStop
	switch {
	case errors.As(cause, &stop):
		why = string(stop)
	case a.lifetime().Err() != nil:
		why = "the identity was stopping"
	case wall:
		why = fmt.Sprintf("it reached the wall of a continued turn (agency.continuation_wall_seconds, %d s)", a.residentLimitsFor(a.configSnapshot()).N(continuationWallSetting))
	}
	a.tellNextPart(sessionID, leg, nil, fmt.Sprintf("The next part of %s was cut short: %s.", a.workName(sessionID), why), "")
}

func (a *App) workName(sessionID string) string {
	if ws, err := a.store.WorkSessionByID(sessionID); err == nil && ws != nil && strings.TrimSpace(ws.Description) != "" {
		return compactText(strings.Join(strings.Fields(ws.Description), " "), noticeNameChars)
	}
	return sessionID
}

func quotedWork(name string) string { return strconv.Quote(name) }

func causeLine(cause error) string {
	return strings.TrimSuffix(compactText(strings.TrimSpace(cause.Error()), noticeCauseChars), ".")
}

func (a *App) tellNextPart(sessionID string, leg int, after func() (string, error), operator, identity string) {
	id := fmt.Sprintf("continuation_%d_%d", leg, time.Now().UTC().UnixNano())
	if err := a.store.AddOutboxNotice(id, "operator", "", operator, nil); err != nil {
		logsink.Warn("continuation.error", "%s: the operator's notice of leg %d could not be written: %v", sessionID, leg, err)
	}
	if identity == "" {
		return
	}
	reply := ""
	if after != nil {
		var err error
		if reply, err = after(); err != nil {
			logsink.Warn("continuation.error", "%s: the reply the line on leg %d stands after could not be read: %v", sessionID, leg, err)
		}
	}
	if reply == "" {
		logsink.Warn("continuation.error", "%s: no recorded reply for the line on leg %d to stand after, so it has no place: %s", sessionID, leg, identity)
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(a.lifetime()), 5*time.Second)
	defer cancel()
	if _, err := a.engine.RecordRelatedConversationRef(ctx, string(interaction.System), identity, reply, interaction.Details{ReasonCode: store.ReasonNextPartNotRun}); err != nil {
		logsink.Warn("continuation.error", "%s: the identity's line on leg %d could not be recorded: %v", sessionID, leg, err)
	}
}

func (a *App) replyOf(turn string) func() (string, error) {
	return func() (string, error) { return a.store.ReplyOfTurn(turn) }
}

const (
	noticeNameChars  = 120
	noticeCauseChars = 300
)

func (a *App) reflectEvery(ctx context.Context) int {
	if a.store == nil {
		return 0
	}
	var ws *store.WorkSession
	var err error
	if id := interaction.ResumeSession(ctx); id != "" {
		ws, err = a.store.WorkSessionByID(id)
	} else {
		ws, err = a.store.ActiveWorkSession()
	}
	if err != nil || ws == nil {
		return 0
	}
	return ws.ReflectEvery
}

const whatMovedSentence = "Another fresh turn follows this one only if it records what moved: state=, evidence=, a delivery, or your promise's state. To stop instead: deliver the work, or set resumes_on to word, a delivery or a time."

func (a *App) predictedThisTurn() (remaining, ordinal int) {
	a.turn.mu.Lock()
	defer a.turn.mu.Unlock()
	return a.turn.turnPredicted, a.turn.turnPredictedOrdinal
}
