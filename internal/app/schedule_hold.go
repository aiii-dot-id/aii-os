package app

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/cognitive"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

const scheduleOwnerName = "schedule"

const scheduleRetryCap = time.Duration(pluginhost.MaxScheduleEvery) * time.Second

const scheduleHoldRetry = time.Minute

func scheduleHoldID(pluginID, operation string) string {
	return "schedule|" + pluginID + "|" + operation
}

type scheduleOwner struct{}

func (scheduleOwner) Name() string { return scheduleOwnerName }

func (scheduleOwner) OnAlarm(context.Context, string, string, int64, string) cognitive.AlarmResult {
	return cognitive.AlarmResult{Accepted: true}
}

var errScheduleHoldForeign = errors.New("a row of another owner stands at this scheduled operation's retry id")

type holdStore interface {
	AlarmByID(alarmID string) (store.Alarm, bool, error)
	SetAlarm(alarmID, ownerName, clock string, deadline int64, repeatEvery *int64, payload string) error
	CancelAlarm(ownerName, alarmID string) error
}

func (a *App) holdStore() holdStore {
	if a.scheduleRows.store != nil {
		return a.scheduleRows.store
	}
	if a.store == nil {
		return nil
	}
	return a.store
}

type scheduleHold struct {
	a                *App
	pluginID, op, id string
	loaded           bool
	want             time.Time
	stored           bool
	removeRow        bool
	removeUntil      time.Time
	malformed        bool
	lastTry          time.Time
}

type scheduleRows struct {
	mu       sync.Mutex
	holds    map[string]*scheduleHold
	store    holdStore
	retryFor time.Duration
}

func (a *App) scheduleHoldFor(pluginID, op string) *scheduleHold {
	a.scheduleRows.mu.Lock()
	defer a.scheduleRows.mu.Unlock()
	id := scheduleHoldID(pluginID, op)
	if h := a.scheduleRows.holds[id]; h != nil {
		return h
	}
	if a.scheduleRows.holds == nil {
		a.scheduleRows.holds = map[string]*scheduleHold{}
	}
	h := newScheduleHold(a, pluginID, op)
	a.scheduleRows.holds[id] = h
	return h
}

func newScheduleHold(a *App, pluginID, op string) *scheduleHold {
	return &scheduleHold{a: a, pluginID: pluginID, op: op, id: scheduleHoldID(pluginID, op)}
}

func (h *scheduleHold) state() (want time.Time, stored bool) {
	h.a.scheduleRows.mu.Lock()
	defer h.a.scheduleRows.mu.Unlock()
	return h.want, h.stored
}

func (h *scheduleHold) load(ctx context.Context, now time.Time) error {
	err, cause := h.loadLocked(ctx, now)
	h.report(cause)
	return err
}

func (h *scheduleHold) loadLocked(ctx context.Context, now time.Time) (blocked, cause error) {
	h.a.scheduleRows.mu.Lock()
	defer h.a.scheduleRows.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err, nil
	}
	if h.loaded && !h.stored {
		return nil, h.persistLocked(ctx, now)
	}
	st := h.a.holdStore()
	if st == nil {
		h.loaded, h.want, h.stored, h.removeRow = true, time.Time{}, true, false
		return nil, nil
	}
	row, ok, err := st.AlarmByID(h.id)
	if err != nil {
		return fmt.Errorf("the retry row could not be read: %w", err), nil
	}
	h.loaded, h.removeRow, h.malformed, h.removeUntil = true, false, false, time.Time{}
	if !ok {
		h.want, h.stored = time.Time{}, true
		return nil, nil
	}
	if row.OwnerName != scheduleOwnerName {
		h.loaded = false
		return errScheduleHoldForeign, nil
	}
	at := time.UnixMilli(row.Deadline)
	limit := now.Add(scheduleRetryCap)
	if at.After(limit) {
		at = limit
	}
	if !at.After(now) {
		at = time.Time{}
	}
	if row.Clock != "wall" || row.RepeatEvery != nil || row.Payload != "" {

		h.want, h.stored, h.removeRow, h.malformed, h.removeUntil, h.lastTry = at, false, true, true, at, time.Time{}
		return nil, h.persistLocked(ctx, now)
	}
	switch {
	case at.IsZero():
		h.want, h.stored = time.Time{}, true
	case at.Before(time.UnixMilli(row.Deadline)):

		logsink.Warn("schedule.error", "%s: %s's saved retry time %s is past the cap of %s; it is kept as %s", h.pluginID, h.op, time.UnixMilli(row.Deadline).UTC().Format(time.RFC3339), scheduleRetryCap, at.UTC().Format(time.RFC3339))
		h.want, h.stored, h.lastTry = at, false, time.Time{}
		return nil, h.persistLocked(ctx, now)
	default:
		h.want, h.stored = at, true
	}
	return nil, nil
}

func (h *scheduleHold) set(ctx context.Context, eff, now time.Time) {
	h.report(h.setLocked(ctx, eff, now))
}

func (h *scheduleHold) setLocked(ctx context.Context, eff, now time.Time) error {
	h.a.scheduleRows.mu.Lock()
	defer h.a.scheduleRows.mu.Unlock()
	if ctx.Err() != nil {
		return nil
	}
	if !h.malformed {
		var changed bool
		if eff.IsZero() {
			changed = !h.want.IsZero() || !h.removeRow
			if !h.want.IsZero() {
				h.removeUntil = h.want
			}
			h.want, h.removeRow = time.Time{}, true
		} else {
			changed = !eff.Equal(h.want) || h.removeRow
			h.want, h.removeRow, h.removeUntil = eff, false, time.Time{}
		}
		if changed {
			h.lastTry = time.Time{}
		}
		h.stored = false
		h.loaded = true
	}
	return h.persistLocked(ctx, now)
}

func (h *scheduleHold) persist(ctx context.Context, now time.Time) {
	h.report(h.persistDue(ctx, now))
}

func (h *scheduleHold) persistDue(ctx context.Context, now time.Time) error {
	h.a.scheduleRows.mu.Lock()
	defer h.a.scheduleRows.mu.Unlock()
	return h.persistLocked(ctx, now)
}

func (a *App) holdRetry() time.Duration {
	if a.scheduleRows.retryFor > 0 {
		return a.scheduleRows.retryFor
	}
	return scheduleHoldRetry
}

func (h *scheduleHold) persistLocked(ctx context.Context, now time.Time) error {
	if ctx.Err() != nil || h.stored {
		return nil
	}

	if !h.lastTry.IsZero() && now.Sub(h.lastTry) < h.a.holdRetry() {
		return nil
	}
	st := h.a.holdStore()
	if st == nil {
		h.stored = true
		return nil
	}
	h.lastTry = now
	var err error
	if h.removeRow {

		if row, ok, rerr := st.AlarmByID(h.id); rerr != nil {
			err = rerr
		} else if ok && row.OwnerName != scheduleOwnerName {
			err = errScheduleHoldForeign
		} else if ok {
			err = st.CancelAlarm(scheduleOwnerName, h.id)
		}
		if err == nil {
			if h.malformed {
				logsink.Warn("schedule.error", "%s: %s's retry row was not one this host writes; it is removed, and the first delay applies", h.pluginID, h.op)
			}
			h.want, h.stored, h.removeRow, h.malformed, h.removeUntil = time.Time{}, true, false, false, time.Time{}
			return nil
		}
		if !h.removeUntil.IsZero() && !now.Before(h.removeUntil) {

			h.want, h.stored, h.removeRow, h.malformed, h.removeUntil = time.Time{}, true, false, false, time.Time{}
			return nil
		}
	} else {
		if h.want.IsZero() || !now.Before(h.want) {
			h.stored = true
			return nil
		}
		if err = st.SetAlarm(h.id, scheduleOwnerName, "wall", h.want.UnixMilli(), nil, ""); err == nil {
			h.stored = true
			return nil
		}
	}

	return fmt.Errorf("the retry row could not be written (%w); the deadline holds in memory only, and is tried again in %s", err, h.a.holdRetry())
}

func (h *scheduleHold) report(cause error) {
	if cause == nil {
		return
	}
	logsink.Warn("schedule.error", "%s: %s: %v", h.pluginID, h.op, cause)
	h.a.reportScheduleFailure(h.pluginID, h.op, cause)
}
