package store

import (
	"database/sql"
	"fmt"
)

// --- Alarms ---

// Alarm is a TIME scheduling entry.
type Alarm struct {
	AlarmID     string
	OwnerName   string
	Clock       string
	Deadline    int64
	RepeatEvery *int64
	Payload     string // opaque to TIME; the owner's bytes (resident timer messages ride here)
}

// SetAlarm inserts or replaces an alarm. A replace never changes the
// alarm's owner: an upsert whose owner differs from the existing row is
// rejected (canon TIME_FACILITIES.md #10).
func (s *Store) SetAlarm(alarmID, ownerName, clock string, deadline int64, repeatEvery *int64, payload string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	res, err := s.db.Exec(
		`INSERT INTO alarms (alarm_id, owner_name, clock, deadline, repeat_every, payload)
		 VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT(alarm_id) DO UPDATE SET
		   clock = excluded.clock,
		   deadline = excluded.deadline,
		   repeat_every = excluded.repeat_every,
		   payload = excluded.payload
		 WHERE alarms.owner_name = excluded.owner_name`,
		alarmID, ownerName, clock, deadline, repeatEvery, payload,
	)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		// Either a no-op same-values upsert or an owner mismatch —
		// distinguish: mismatch is an error, idempotent set is fine.
		var existing string
		err := s.db.QueryRow(`SELECT owner_name FROM alarms WHERE alarm_id = ?`, alarmID).Scan(&existing)
		if err == nil && existing != ownerName {
			return fmt.Errorf("set alarm %s: owner mismatch (row owner %q, caller %q) — replacing never changes owner", alarmID, existing, ownerName)
		}
	}
	return nil
}

// CancelAlarm removes an alarm the caller OWNS.
//
// A DELETE that deleted nothing is not a cancellation. This returned nil
// on zero rows, so the identity saying `timer cancel id=X` was told
// "Timer X cancelled." for a timer that did not exist, or one belonging
// to another owner — and the timer it meant to stop went on to fire.
// Same shape as "Sent to peer.": a report of an act that did not happen.
//
// The two zero-row cases are told apart, because "you have no such
// timer" and "that one is not yours" send the identity somewhere
// different.
func (s *Store) CancelAlarm(ownerName, alarmID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	res, err := s.db.Exec(
		`DELETE FROM alarms WHERE alarm_id = ? AND owner_name = ?`,
		alarmID, ownerName,
	)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("cancel alarm %s: cannot tell whether it was removed: %w", alarmID, err)
	}
	if n > 0 {
		return nil
	}
	var existing string
	switch err := s.db.QueryRow(`SELECT owner_name FROM alarms WHERE alarm_id = ?`, alarmID).Scan(&existing); {
	case err == sql.ErrNoRows:
		return fmt.Errorf("no alarm %q to cancel", alarmID)
	case err != nil:
		return fmt.Errorf("cancel alarm %s: nothing was removed: %w", alarmID, err)
	default:
		return fmt.Errorf("alarm %q belongs to %q, not %q — nothing was cancelled", alarmID, existing, ownerName)
	}
}

// DueAlarms returns alarms that are due on the given clock, ordered by deadline.
func (s *Store) DueAlarms(clock string, nowOrLess int64, limit int) ([]Alarm, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.Query(
		`SELECT alarm_id, owner_name, clock, deadline, repeat_every, payload
		 FROM alarms WHERE clock = ? AND deadline <= ?
		 ORDER BY deadline ASC, alarm_id ASC LIMIT ?`,
		clock, nowOrLess, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var alarms []Alarm
	for rows.Next() {
		var a Alarm
		var repeat sql.NullInt64
		var payload sql.NullString
		if err := rows.Scan(&a.AlarmID, &a.OwnerName, &a.Clock, &a.Deadline, &repeat, &payload); err != nil {
			return nil, err
		}
		if repeat.Valid {
			v := repeat.Int64
			a.RepeatEvery = &v
		}
		a.Payload = payload.String
		alarms = append(alarms, a)
	}
	return alarms, rows.Err()
}

// DeleteAlarm removes an alarm by ID (after one-shot acceptance).
func (s *Store) DeleteAlarm(alarmID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.Exec(`DELETE FROM alarms WHERE alarm_id = ?`, alarmID)
	return err
}

// UpdateAlarmDeadlineCAS reschedules an alarm ONLY if its deadline still
// matches the scheduled one — the dispatch law's compare-and-swap (a
// stale firing attempt must not apply its transition to a replaced row).
// Returns whether the transition applied.
func (s *Store) UpdateAlarmDeadlineCAS(alarmID string, expectedDeadline, newDeadline int64) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	res, err := s.db.Exec(
		`UPDATE alarms SET deadline = ? WHERE alarm_id = ? AND deadline = ?`,
		newDeadline, alarmID, expectedDeadline,
	)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// DeleteAlarmCAS deletes an alarm ONLY if its deadline still matches the
// scheduled one (see UpdateAlarmDeadlineCAS).
func (s *Store) DeleteAlarmCAS(alarmID string, expectedDeadline int64) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	res, err := s.db.Exec(
		`DELETE FROM alarms WHERE alarm_id = ? AND deadline = ?`,
		alarmID, expectedDeadline,
	)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}
