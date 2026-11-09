package app

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/identity"
	"github.com/aiii-dot-id/aii-os/internal/interaction"
	"github.com/aiii-dot-id/aii-os/internal/limits"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

func (a *App) operatorStage() string {
	s, err := a.stageRead()
	if err != nil {

		logsink.Warn("work.error", "the operator's stage could not be read: %v", err)
		return ""
	}
	return s
}

func (a *App) stageRead() (string, error) {
	if a.stageReader != nil {
		return a.stageReader()
	}
	if a.store == nil {
		return store.StageSupervised, nil
	}
	s, err := a.store.ApprovedStage()
	if err != nil {
		return "", err
	}
	if s == "" {
		s = store.StageAutonomous
	}
	return s, nil
}

func (a *App) heldByOperator() (bool, error) {
	s, err := a.stageRead()
	return s == store.StageSupervised, err
}

func (a *App) stageUnread(ws *store.WorkSession, turn string, err error) {
	logsink.Warn("continuation.error", "%s: the operator's stage could not be read; the work is tried again at the next tick: %v", ws.ID, err)
	a.setResumesOn(ws.ID, turn, "now", "The operator's stage could not be read; the fresh turn waits for the next tick")
}

const stageUnreadWhy = "the host could not read your operator's stage; the work is tried again at the next tick"

func operatorProvenance(source string) bool {
	return source == turnSourceOperator || source == turnSourceSteer
}

func (a *App) promisedUnattended(ws *store.WorkSession) bool {
	turn := ws.PromisedTurn
	if turn == "" {

		_, _, _, t, err := a.store.PromiseChangedAt(ws.ID)
		if err != nil {
			return false
		}
		turn = t
	}
	source, _, err := a.store.TurnSourceOf(turn)
	return err == nil && source != "" && source != turnSourceArrival
}

func (a *App) sessionOfTurn(turn string) (*store.WorkSession, error) {
	if a.store == nil {
		return nil, nil
	}
	if turn != "" {
		_, _, session, _, err := a.store.TurnOpening(turn)
		if err != nil {
			return nil, err
		}
		if session != "" {
			ws, err := a.store.WorkSessionByID(session)
			if err != nil || ws == nil || ws.Status != "active" {
				return nil, err
			}
			return ws, nil
		}
	}
	return a.store.ActiveWorkSession()
}

func (a *App) sessionAwaiting(child string) (*store.WorkSession, error) {
	return a.keptWhere(func(ws *store.WorkSession) bool { return ws.ResumesOn == "delivery:"+child })
}

func (a *App) sessionsAwaiting(child string) []store.WorkSession {
	if a.store == nil {
		return nil
	}
	kept, err := a.store.KeptPromisedSessions()
	if err != nil {
		logsink.Warn("work.error", "the sessions waiting on %s's delivery could not be read: %v", child, err)
		return nil
	}
	var out []store.WorkSession
	for _, ws := range kept {
		if ws.ResumesOn == "delivery:"+child {
			out = append(out, ws)
		}
	}
	return out
}

func (a *App) sessionOfFiring(alarmID string, deadline int64) (*store.WorkSession, error) {
	return a.keptWhere(func(ws *store.WorkSession) bool {
		return identity.PromiseAlarmID(ws.ID) == alarmID && ws.ResumesOn == "time:"+strconv.FormatInt(deadline, 10)
	})
}

func (a *App) conditionSetIn(ws *store.WorkSession) string {
	_, _, _, turn, err := a.store.PromiseChangedAt(ws.ID)
	if err != nil {
		return ""
	}
	source, _, _ := a.store.TurnSourceOf(turn)
	return source
}

func (a *App) keptWhere(match func(*store.WorkSession) bool) (*store.WorkSession, error) {
	if a.store == nil {
		return nil, nil
	}
	kept, err := a.store.KeptPromisedSessions()
	if err != nil {
		logsink.Warn("work.error", "the sessions kept under a condition could not be read: %v", err)
		return nil, err
	}
	for i := range kept {
		if match(&kept[i]) {
			return &kept[i], nil
		}
	}
	return nil, nil
}

func (a *App) holdPromise(ws *store.WorkSession, turn, came string) {
	a.setResumesOn(ws.ID, turn, "word", "Held by the operator (supervised): "+came+"; the work resumes on their word")
	if err := a.store.AddOutboxNotice(fmt.Sprintf("held_%s_%d", ws.ID, time.Now().UTC().UnixNano()), "operator", "",
		fmt.Sprintf("%s: %s. It waits for your word.", a.workName(ws.ID), came), nil); err != nil {
		logsink.Warn("continuation.error", "%s: the hold's notice could not be written: %v", ws.ID, err)
	}
}

func (a *App) holdOnStop(turn string) {
	if a.store == nil {
		return
	}
	ws, err := a.sessionOfTurn(turn)
	if err != nil || ws == nil || turn == "" {
		return
	}
	source, _, err := a.store.TurnSourceOf(turn)
	if err != nil || (source != turnSourceContinuation && source != turnSourceTimer && source != turnSourceHarvest) {
		return
	}
	a.setResumesOn(ws.ID, turn, "word", "Held by the operator: they stopped the turn that was resuming this work; it resumes on their word")
}

func (a *App) spentToday() int {
	if a.store == nil || a.cfg == nil {
		return 0
	}
	zone := a.speechZone()
	now := time.Now().In(zone)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, zone)
	rows, err := a.store.SpendSince(today.UnixMilli())
	if err != nil {
		return 0
	}
	tokens := 0
	for _, r := range rows {
		if r.Known {
			tokens += r.Tokens
		}
	}
	return tokens
}

func (a *App) dailyCeiling() limits.Ceiling {
	if a.cfg == nil {
		return limits.Ceiling{Unlimited: true}
	}
	c := a.configSnapshot().Agency.DailyTokenCeiling
	if c.IsZero() {
		return limits.Ceiling{Unlimited: true}
	}
	return c
}

func (a *App) wideningCompletions() int {
	if a.cfg == nil {
		return limits.DefaultWideningCompletions
	}
	if n := a.configSnapshot().Agency.AutonomyWideningCompletions; n > 0 {
		return n
	}
	return limits.DefaultWideningCompletions
}

func (a *App) pastCeiling() (past bool, resumeAt int64) {
	ceiling := a.dailyCeiling()
	if ceiling.Allows(a.spentToday()) {
		return false, 0
	}
	zone := a.zoneOrUTC()
	now := time.Now().In(zone)
	tomorrow := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, zone).AddDate(0, 0, 1)
	return true, tomorrow.UnixMilli()
}

func (a *App) zoneOrUTC() *time.Location {
	if a.cfg == nil {
		return time.UTC
	}
	return a.speechZone()
}

func (a *App) waitForTomorrow(ws *store.WorkSession, turn string, at int64) {
	a.setResumesOn(ws.ID, turn, "time:"+strconv.FormatInt(at, 10), "Today's ceiling (agency.daily_token_ceiling) is reached; the work resumes tomorrow or on the operator's word")
}

const ceilingWhy = "today's ceiling is reached; this work resumes tomorrow or on your operator's word"

func (a *App) continueWork(ws *store.WorkSession, reason capReason, from string) turnEnding {
	what := "this work"
	if ws.Promise != "" {
		what = ws.Promise
	}
	held, err := a.heldByOperator()
	if err != nil {
		a.stageUnread(ws, from, err)
		return turnEnding{nothingFollows, stageUnreadWhy}
	}
	if held {
		a.holdPromise(ws, from, "the turn ended with "+what+" still active")
		return turnEnding{nothingFollows, "your operator holds this work; it resumes on their word"}
	}
	if past, at := a.pastCeiling(); past {
		a.waitForTomorrow(ws, from, at)
		return turnEnding{nothingFollows, ceilingWhy}
	}
	stuck, _, err := a.store.SessionStuck(ws.ID, store.DefaultStuckThresholds)
	if err != nil {
		logsink.Warn("continuation.error", "%s: whether the work is stuck could not be read: %v", ws.ID, err)
		return turnEnding{nothingFollows, "the host could not read the session's record"}
	}
	if stuck != "" {
		a.stuckWork(ws, from, stuck)
		return turnEnding{nothingFollows, "the record shows this work stuck (" + stuck + "); it resumes on your operator's word"}
	}
	prev, at, since, _, err := a.store.PromiseChangedAt(ws.ID)
	if err != nil {
		logsink.Warn("continuation.error", "%s: the condition's record could not be read: %v", ws.ID, err)
		return turnEnding{nothingFollows, "the host could not read the condition's record"}
	}

	a.turn.mu.Lock()
	queued := a.turn.legsQueued > 0
	a.turn.mu.Unlock()
	if queued {
		return turnEnding{kind: continued}
	}
	if prev == "now" && a.freshTurnOf(ws, from) {

		progressed, err := a.store.SessionProgressSince(ws.ID, since)
		if err == nil && !progressed && ws.Promise != "" {
			progressed, err = a.store.PromiseProgressSince(ws.Promise, at)
		}
		if err != nil {
			logsink.Warn("continuation.error", "%s: progress could not be read: %v", ws.ID, err)
			return turnEnding{nothingFollows, "the host could not read the session's progress"}
		}
		if !progressed {
			a.setResumesOn(ws.ID, from, "word", "No progress on "+what+" was recorded by the fresh turn; the work resumes on the operator's word")
			return turnEnding{nothingFollows, "no progress on " + what + " was recorded since its last fresh turn; it resumes on your operator's word"}
		}
	}
	a.setResumesOn(ws.ID, from, "now", "The turn ended "+reason.how()+" with "+what+" still active; a fresh turn follows")
	a.turn.mu.Lock()
	a.turn.contChain++
	leg := a.turn.contChain
	a.turn.mu.Unlock()
	a.addQueuedLegs(1)
	if !a.runBackground(func() { a.continueCappedTurn(ws.ID, leg, reason, from, true, ws.Promise) }) {
		a.addQueuedLegs(-1)
		a.setResumesOn(ws.ID, from, "word", "The identity is stopping; the work resumes on the operator's word")
		logsink.Info("continuation.refusal", "not scheduled for %s: runtime stopping", ws.ID)
		return turnEnding{nothingFollows, "the identity is stopping"}
	}
	return turnEnding{kind: continued}
}

func (a *App) freshTurnOf(ws *store.WorkSession, turn string) bool {
	if turn == "" {
		return false
	}
	source, _, session, _, err := a.store.TurnOpening(turn)
	return err == nil && source == turnSourceContinuation && (session == "" || session == ws.ID)
}

func (a *App) stuckWork(ws *store.WorkSession, turn, pattern string) {
	a.setResumesOn(ws.ID, turn, "word", "Stuck: "+pattern+"; the work resumes on the operator's word")
	if err := a.store.AddOutboxNotice(fmt.Sprintf("stuck_%s_%d", ws.ID, time.Now().UTC().UnixNano()), "operator", "",
		fmt.Sprintf("%s: the record shows the work stuck (%s). It waits for your word.", a.workName(ws.ID), pattern), nil); err != nil {
		logsink.Warn("continuation.error", "%s: the stuck notice could not be written: %v", ws.ID, err)
	}
}

type heldRelease struct {
	why    string
	reason capReason
}

var (
	releasedAtRestart = heldRelease{why: "the identity restarted", reason: capRestart}
	releasedAtTick    = heldRelease{why: "the metabolism's tick", reason: capTick}
)

func (a *App) releaseHeldPromise(by heldRelease) {
	if a.store == nil {
		return
	}
	kept, err := a.store.KeptPromisedSessions()
	if err != nil {
		return
	}

	for i := range kept {
		ws := &kept[i]
		if ws.ResumesOn != "now" {
			continue
		}
		if a.releaseNow(ws, by) {
			return
		}
	}
}

func (a *App) releaseNow(ws *store.WorkSession, by heldRelease) bool {
	why := by.why
	prev, _, since, turn, err := a.store.PromiseChangedAt(ws.ID)
	if err != nil || prev != "now" {
		return false
	}
	if ws.Promise != "" && !a.promisedUnattended(ws) {
		a.setResumesOn(ws.ID, turn, "word", "The turn that made this promise was an arrival's, or is not in the record; the work resumes on the operator's word")
		return false
	}
	held, err := a.heldByOperator()
	if err != nil {

		logsink.Warn("continuation.error", "%s: the operator's stage could not be read; the `now` waits for the next tick: %v", ws.ID, err)
		return false
	}
	if held {
		a.holdPromise(ws, turn, "a fresh turn was to carry "+ws.Promise+" on")
		return false
	}
	if past, at := a.pastCeiling(); past {
		a.waitForTomorrow(ws, turn, at)
		return false
	}
	ran, err := a.store.ContinuationRanSince(ws.ID, since)
	if err != nil {
		logsink.Warn("continuation.error", "%s: whether its fresh turn ran could not be read: %v", ws.ID, err)
		return false
	}
	if ran {
		a.setResumesOn(ws.ID, turn, "word", fmt.Sprintf("The fresh turn that was to carry this work on was interrupted (%s); nothing was repeated. It resumes on the operator's word", why))
		return false
	}
	a.turn.mu.Lock()
	queued := a.turn.legsQueued > 0
	if !queued {
		a.turn.contChain++
	}
	leg := a.turn.contChain
	a.turn.mu.Unlock()
	if queued {
		return true
	}
	a.addQueuedLegs(1)
	if !a.runBackground(func() { a.continueCappedTurn(ws.ID, leg, by.reason, turn, true, ws.Promise) }) {
		a.addQueuedLegs(-1)
		return false
	}
	return true
}

func (a *App) settleMetConditions(from string) {
	if a.store == nil || from == "" {
		return
	}
	ws, err := a.sessionOfTurn(from)
	if err != nil || ws == nil {
		return
	}
	source, _, session, consumed, err := a.store.TurnOpening(from)
	if err != nil || source != turnSourceTimer || session != ws.ID {
		return
	}
	ms, ok := strings.CutPrefix(ws.ResumesOn, "time:")
	if !ok {
		return
	}
	at, err := strconv.ParseInt(ms, 10, 64)
	if err != nil || at != consumed || at > time.Now().UTC().UnixMilli() {
		return
	}
	if _, pending, err := a.store.AlarmByID(identity.PromiseAlarmID(ws.ID)); err != nil || pending {
		return
	}
	a.conditionMet(ws.ID, from, ws.ResumesOn, "The moment named with resumes_on came; the work resumed in this turn")
}

func (a *App) wordCame() {
	if a.store == nil {
		return
	}
	ws, err := a.store.ActiveWorkSession()
	if err != nil || ws == nil || ws.ResumesOn != "word" {
		return
	}
	a.conditionMet(ws.ID, "", "word", "The operator's word came; the work resumed in this turn")
}

func (a *App) deliveryHeld(child string) (bool, *store.WorkSession) {
	ws, err := a.sessionAwaiting(child)
	if err != nil {

		return true, nil
	}
	if ws == nil {
		return false, nil
	}
	if !a.promisedUnattended(ws) {
		a.setResumesOn(ws.ID, "", "word", "The turn that made this promise was an arrival's, or is not in the record; sub-agent "+child+" delivered, and the work resumes on the operator's word")
		return true, nil
	}
	held, err := a.heldByOperator()
	if err != nil {
		a.stageUnread(ws, "", err)
		return true, nil
	}
	if held {
		a.holdPromise(ws, "", "sub-agent "+child+" delivered")
		return true, nil
	}
	if past, at := a.pastCeiling(); past {
		a.waitForTomorrow(ws, "", at)
		return true, nil
	}
	return false, ws
}

func (a *App) deliveryCame(ws *store.WorkSession) (line, session string) {
	if ws == nil {
		return "", ""
	}
	_, under := a.conditionWords(ws)

	if under == "" {
		return " This delivery is what your work on " + ws.ID + " waited for.", ws.ID
	}
	return " This delivery is what your work on " + ws.ID + " waited for, under " + under + ".", ws.ID
}

func (a *App) childEndedUnfinished(child, cause string) {
	for _, ws := range a.sessionsAwaiting(child) {
		var never *store.ErrDeliveryNeverComes
		err := a.store.DeliveryNeverComes(a.lifetime(), ws.ID, "delivery:"+child)
		if err != nil && !errors.As(err, &never) {

			logsink.Warn("work.error", "%s: whether sub-agent %s's delivery can still come could not be read (%v); its wait stands", ws.ID, child, err)
			continue
		}
		if never == nil {
			continue
		}
		a.setResumesOn(ws.ID, "", "", "Sub-agent "+child+" ended unfinished ("+cause+"); nothing resumes this work until the identity says what does")
	}
}

func (a *App) promiseConditionsAtBoot() {
	kept, err := a.store.KeptPromisedSessions()
	if err != nil {
		logsink.Warn("work.error", "the sessions kept under a promise could not be read: %v", err)
		return
	}
	for _, ws := range kept {
		child, ok := strings.CutPrefix(ws.ResumesOn, "delivery:")
		if !ok {
			continue
		}
		var never *store.ErrDeliveryNeverComes
		if err := a.store.DeliveryNeverComes(a.lifetime(), ws.ID, ws.ResumesOn); errors.As(err, &never) {
			a.setResumesOn(ws.ID, "", "", "Sub-agent "+child+" did not survive the restart: "+never.Why+"; nothing resumes this work until the identity says what does")
		}
	}

	if alarms, err := a.store.DueAlarms("wall", int64(1)<<62, 100000); err == nil {
		for _, al := range alarms {
			sid, ok := strings.CutPrefix(al.AlarmID, "promise_")
			if !ok || al.OwnerName != "timers" {
				continue
			}
			ws, werr := a.store.WorkSessionByID(sid)
			if werr == nil && ws != nil && ws.Status == "active" && ws.Promise != "" && ws.ResumesOn == "time:"+strconv.FormatInt(al.Deadline, 10) {
				continue
			}
			if err := a.store.CancelAlarm("timers", al.AlarmID); err != nil {
				logsink.Warn("work.error", "%s: a promise alarm no condition owns could not be cancelled at boot: %v", al.AlarmID, err)
			}
		}
	}
	for _, ws := range kept {
		ms, ok := strings.CutPrefix(ws.ResumesOn, "time:")
		if !ok {
			continue
		}
		at, perr := strconv.ParseInt(ms, 10, 64)
		id := identity.PromiseAlarmID(ws.ID)
		alarm, found, aerr := a.store.AlarmByID(id)
		if perr != nil || aerr != nil || (found && alarm.Deadline == at) {
			continue
		}
		setIn := ""
		if _, _, _, turn, err := a.store.PromiseChangedAt(ws.ID); err == nil {
			setIn, _, _ = a.store.TurnSourceOf(turn)
		}
		if err := a.store.SetAlarm(id, "timers", "wall", at, nil, identity.PromiseAlarmPayload(ws.ID, setIn)); err != nil {
			logsink.Warn("work.error", "%s: the condition's alarm could not be repaired at boot: %v", ws.ID, err)
		}
	}
	a.bootKept = kept
}

func (a *App) keptSessionNote() string {
	var lines []string
	for _, kept := range a.bootKept {
		ws, err := a.store.WorkSessionByID(kept.ID)
		if err != nil || ws == nil {
			ws = &kept
		}
		if ws.ResumesOn == "" {
			lines = append(lines, fmt.Sprintf("Work session %s (%q) was active at the last shutdown and stays open; its sub-agent did not survive the restart, so nothing resumes it until you say what does. Nothing was repeated. Verify what the record shows before continuing.", ws.ID, ws.Description))
			continue
		}
		on, under := a.conditionWords(ws)
		if under == "" {
			lines = append(lines, fmt.Sprintf("Work session %s (%q) was active at the last shutdown and stays open: it resumes on %s. Nothing was repeated. Verify what the record shows before continuing.", ws.ID, ws.Description, on))
			continue
		}
		lines = append(lines, fmt.Sprintf("Work session %s (%q) was active at the last shutdown and stays open: it resumes on %s under %s. Nothing was repeated. Verify what the record shows before continuing.", ws.ID, ws.Description, on, under))
	}
	return strings.Join(lines, "\n")
}

func (a *App) conditionMet(sessionID, turn, met, reason string) {
	ctx, cancel := context.WithTimeout(interaction.WithTurn(context.WithoutCancel(a.lifetime()), turn), 5*time.Second)
	defer cancel()
	if err := a.store.MeetWorkCondition(ctx, sessionID, met, reason); err != nil {
		logsink.Warn("continuation.error", "%s: the met condition could not be cleared: %v", sessionID, err)
		return
	}
	_ = a.store.CancelAlarm("timers", identity.PromiseAlarmID(sessionID))
}

func (a *App) setResumesOn(sessionID, turn, on, reason string) {
	ctx, cancel := context.WithTimeout(interaction.WithTurn(context.WithoutCancel(a.lifetime()), turn), 5*time.Second)
	defer cancel()
	if err := a.store.SetWorkPromise(ctx, sessionID, nil, &on, reason); err != nil {
		logsink.Warn("continuation.error", "%s: resumes_on could not be set to %s: %v", sessionID, on, err)
		return
	}

	if ms, ok := strings.CutPrefix(on, "time:"); ok {
		if at, err := strconv.ParseInt(ms, 10, 64); err == nil {
			setIn, _, _ := a.store.TurnSourceOf(turn)
			if err := a.store.SetAlarm(identity.PromiseAlarmID(sessionID), "timers", "wall", at, nil, identity.PromiseAlarmPayload(sessionID, setIn)); err != nil {
				logsink.Warn("continuation.error", "%s: the condition's alarm could not be set: %v", sessionID, err)
			}
		}
		return
	}
	_ = a.store.CancelAlarm("timers", identity.PromiseAlarmID(sessionID))
}
