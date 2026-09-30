package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/identity"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

var timerWakeBudget = 15 * time.Minute

const safeWakePosture = " You are in safe mode: your integrity is unverified, nothing you say or do now is recorded, and only your read-only tools work."

func timerNotice(alarmID, tag, message string, at time.Time) string {
	notice := fmt.Sprintf("[timer %s", alarmID)
	if tag != "" {
		notice += fmt.Sprintf(" #%s", tag)
	}
	notice += fmt.Sprintf(" fired %s]", at.UTC().Format("15:04:05 MST Mon Jan 2"))
	if message != "" {
		notice += " " + message
	}
	return notice
}

type timerRecordKey struct{}
type timerResumeKey struct{}

type timerWakeHandler struct{ a *App }

func (*timerWakeHandler) WorkKinds() []string { return []string{identity.TimerWakeWorkKind} }
func (h *timerWakeHandler) RunWork(ctx context.Context, w *store.WorkItem) error {
	var firing identity.TimerWake
	if err := json.Unmarshal([]byte(w.Payload), &firing); err != nil {
		return fmt.Errorf("timer wake payload: %w", err)
	}
	if firing.AlarmID == "" || w.ID != "wake_"+firing.DeliveryID() {
		return fmt.Errorf("timer wake does not name its queued firing")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	notice, err := h.a.store.OutboxContent(firing.DeliveryID())
	if err != nil {
		return fmt.Errorf("timer %s recorded notice unavailable: %w", firing.AlarmID, err)
	}
	notice = h.a.timerOwner.NoticeAfterRestore(firing.Deadline, notice)
	ctx = context.WithValue(ctx, timerRecordKey{}, firing.DeliveryID())
	ctx = context.WithValue(ctx, timerResumeKey{}, w.RetryCount > 0)
	return h.a.wakeTimerAlarm(ctx, firing.AlarmID, notice)
}

func (a *App) wakeTimerAlarm(ctx context.Context, alarmID, notice string) error {

	if err := a.acquireTurn(ctx); err != nil {
		return fmt.Errorf("timer %s could not take the turn: %w", alarmID, err)
	}
	defer a.releaseTurn()
	if err := ctx.Err(); err != nil {
		return err
	}
	if reason, safe := a.SafeMode(); safe {
		return &store.FrozenError{Reason: reason}
	}

	turnBase := withTurnSource(ctx, turnSourceTimer)
	turnCtx, cancelTurn := context.WithTimeout(turnBase, timerWakeBudget)
	defer cancelTurn()
	fact := notice + " — your own alarm woke you."
	if resumed, _ := ctx.Value(timerResumeKey{}).(bool); resumed {

		fact += " You are resuming an interrupted timer wake. Read the earlier delivery outcomes before deciding whether another send is needed."
	}
	if _, safe := a.SafeMode(); safe {

		fact += safeWakePosture
	}
	spoken, recorded, err := a.wakeRecorded(turnCtx, "system", fact+" Respond to your operator in your own words.")
	if err != nil {
		if errors.Is(err, context.Canceled) && ctx.Err() == nil {

			logsink.Warn("wake.error", "timer %s wake cancelled; this attempt will not be retried", alarmID)
			return nil
		}
		return fmt.Errorf("timer %s wake turn failed: %w", alarmID, err)
	}
	if spoken == "" {
		return nil
	}
	wakeID := fmt.Sprintf("wake_%s_%d", alarmID, time.Now().UTC().UnixNano())
	if err := a.store.AddOutboxMessageForInteraction(wakeID, "operator", "", spoken, recorded); err != nil {

		var frozen *store.FrozenError
		if errors.As(err, &frozen) {
			a.deliverTransient(wakeID, spoken)
		} else {
			return fmt.Errorf("timer %s wake reply delivery failed: %w", alarmID, err)
		}
	}
	logsink.Info("wake.end", "TIMER WAKE: %s woke and spoke (%d chars)", alarmID, len(spoken))
	return nil
}

func (a *App) drainSafeWakes(ctx context.Context, q *identity.SafeWakes) {
	for {
		a.announceSafeFirings(q.Fresh())
		if q.Idle() {
			return
		}
		got, err := a.acquireTurnOr(ctx, q.Arrived())
		if err != nil {

			a.announceSafeFirings(q.Fresh())
			dropped := q.Abandon()
			logsink.Warn("wake.error", "(SAFE): could not take the turn for %d timer(s) — their notices were delivered transiently and the rows fire again on recovery: %v", len(dropped), err)
			return
		}
		if !got {
			continue
		}
		if batch := q.Take(); len(batch) > 0 {
			a.runSafeWake(batch)
		}
		a.releaseTurn()
	}
}

func (a *App) announceSafeFirings(fs []identity.SafeFiring) {
	if len(fs) == 0 {
		return
	}
	items := make([]dashboard.OutboxItem, len(fs))
	for i, f := range fs {
		content := timerNotice(f.AlarmID, f.Tag, f.Message, f.FiredAt)
		if f.Coalesced > 0 {
			content += fmt.Sprintf(" (it also fired %d time(s) earlier while waiting)", f.Coalesced)
		}
		items[i] = dashboard.OutboxItem{ID: fmt.Sprintf("timer_%s_%d", f.AlarmID, f.Deadline), To: "operator", Content: content}
	}
	if a.dashboard != nil && a.dashboard.PushTransientItems(items) > 0 {
		return
	}
	logsink.Info("wake.decision", "(SAFE) nobody connected — %d timer notice(s) were transient-only: %s", len(fs), firingIDs(fs))
}

func (a *App) runSafeWake(batch []identity.SafeFiring) {
	turnCtx, cancelTurn := context.WithTimeout(withTurnSource(context.Background(), turnSourceTimer), timerWakeBudget)
	defer cancelTurn()
	notices := make([]string, len(batch))
	for i, f := range batch {
		notices[i] = timerNotice(f.AlarmID, f.Tag, f.Message, f.FiredAt)
	}
	var fact string
	if len(batch) == 1 {
		fact = notices[0] + " — your own alarm woke you."
	} else {
		fact = strings.Join(notices, "\n") + fmt.Sprintf("\n— %d of your own alarms woke you.", len(batch))
	}
	fact += safeWakePosture + " Respond to your operator in your own words."
	wakeID := fmt.Sprintf("wake_%s_%d_safe", batch[0].AlarmID, batch[0].Deadline)
	spoken, err := a.wake(turnCtx, "system", fact)
	if err != nil {

		logsink.Warn("wake.error", "turn failed for %d timer(s) (SAFE, notices delivered transiently, no retry): %v", len(batch), err)
		a.deliverTransient(wakeID, fmt.Sprintf("[timer %s fired in safe mode, but the wake turn did not finish: %v — it fires again once the operator restores the identity]", firingIDs(batch), err))
		return
	}
	if spoken == "" {
		return
	}
	a.deliverTransient(wakeID, spoken)
	logsink.Info("wake.end", "TIMER WAKE (SAFE): %d timer(s) woke the identity and it spoke (%d chars) — transient, nothing written", len(batch), len(spoken))
}

func firingIDs(fs []identity.SafeFiring) string {
	ids := make([]string, len(fs))
	for i, f := range fs {
		ids[i] = f.AlarmID
	}
	return strings.Join(ids, ", ")
}

func (a *App) deliverTransient(id, content string) {
	if a.dashboard == nil {
		logsink.Info("wake.decision", "(SAFE, no dashboard) %s: %s", id, content)
		return
	}
	if n := a.dashboard.PushTransient(id, content); n == 0 {
		logsink.Info("wake.decision", "(SAFE) nobody connected — %s was transient-only: %s", id, content)
	}
}
