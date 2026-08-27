package identity

import (
	"fmt"
	"strings"

	"github.com/aiii-dot-id/aii-os/internal/store"
)

// storeTimers adapts the alarm table to the timer verb: resident timers
// are durable wall rows owned by the "timers" delivery owner, payload =
// the message (opaque to TIME). One-shot by construction (no repeat).
type storeTimers struct{ st *store.Store }

// NewStoreTimers builds the timer backend over the alarm table.
func NewStoreTimers(st *store.Store) TimerSetter { return &storeTimers{st: st} }

func (s *storeTimers) SetTimer(id, payload string, deadline int64) error {
	return s.st.SetAlarm(id, "timers", "wall", deadline, nil, payload)
}

// SetRepeating arms a recurring resident alarm (repeat_every rides the
// durable row; TIME's dispatch law rearms natively).
func (s *storeTimers) SetRepeating(id, payload string, deadline int64, every int64) error {
	return s.st.SetAlarm(id, "timers", "wall", deadline, &every, payload)
}

func (s *storeTimers) CancelTimer(id string) error {
	return s.st.CancelAlarm("timers", id)
}

// ListTimers returns PENDING + OVERDUE rows from the alarm table and
// FIRED timers from the outbox (the durable trace — an alarm system
// without history is a bookmark system).
func (s *storeTimers) ListTimers() ([]TimerInfo, error) {
	var out []TimerInfo
	alarms, err := s.st.DueAlarms("wall", int64(1)<<62, 1000)
	if err != nil {
		return nil, fmt.Errorf("list pending timers: %w", err)
	}
	for _, a := range alarms {
		if a.OwnerName == "timers" {
			tag, msg := decodeTimerPayload(a.Payload)
			out = append(out, TimerInfo{ID: a.AlarmID, Tag: tag, Deadline: a.Deadline, Message: msg})
		}
	}
	fired, err := s.st.TimerFiringsSince(0)
	if err != nil {
		return nil, fmt.Errorf("list fired timers: %w", err)
	}
	for _, f := range fired {
		id := strings.TrimPrefix(f.ID, "timer_")
		if i := strings.LastIndexByte(id, '_'); i > 0 {
			id = id[:i] // strip the per-firing suffix, preserving '_' in the alarm id
		}
		// The outbox copy is already human text ("[timer t_x #tag fired ...] msg")
		out = append(out, TimerInfo{ID: id, Message: f.Content, Fired: true, FiredAtMs: f.CreatedMs})
	}
	return out, nil
}

// ensureTimer guards the verb when no backend is wired (tests): honest
// refusal, never a silent no-op.
func (e *Engine) ensureTimer() error {
	if e.timers == nil {
		return fmt.Errorf("timers are not available in this runtime")
	}
	return nil
}
