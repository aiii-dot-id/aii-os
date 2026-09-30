package store

import (
	"database/sql"
	"errors"
	"fmt"
)

type Alarm struct {
	AlarmID     string
	OwnerName   string
	Clock       string
	Deadline    int64
	RepeatEvery *int64
	Payload     string
}

func (s *Store) SetAlarm(alarmID, ownerName, clock string, deadline int64, repeatEvery *int64, payload string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.setAlarmLocked(alarmID, ownerName, clock, deadline, repeatEvery, payload)
}

func (s *Store) EnsureAlarm(alarmID, ownerName, clock string, deadline int64, repeatEvery *int64, payload string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var oldOwner, oldClock, oldPayload string
	var oldDeadline int64
	var oldRepeat sql.NullInt64
	err := s.w().QueryRow(`SELECT owner_name, clock, deadline, repeat_every, payload FROM alarms WHERE alarm_id = ?`, alarmID).
		Scan(&oldOwner, &oldClock, &oldDeadline, &oldRepeat, &oldPayload)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("read alarm %s before arming: %w", alarmID, err)
	}
	sameRepeat := repeatEvery == nil && !oldRepeat.Valid || repeatEvery != nil && oldRepeat.Valid && *repeatEvery == oldRepeat.Int64
	if err == nil && oldOwner == ownerName && oldClock == clock {
		if oldPayload == payload && sameRepeat {
			return nil
		}
		deadline = oldDeadline
	}
	return s.setAlarmLocked(alarmID, ownerName, clock, deadline, repeatEvery, payload)
}

func (s *Store) setAlarmLocked(alarmID, ownerName, clock string, deadline int64, repeatEvery *int64, payload string) error {
	res, err := s.w().Exec(
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

		var existing string
		err := s.db.QueryRow(`SELECT owner_name FROM alarms WHERE alarm_id = ?`, alarmID).Scan(&existing)
		if err == nil && existing != ownerName {
			return fmt.Errorf("set alarm %s: owner mismatch (row owner %q, caller %q) — replacing never changes owner", alarmID, existing, ownerName)
		}
	}
	return nil
}

func (s *Store) CancelAlarm(ownerName, alarmID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	res, err := s.w().Exec(
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
	return scanAlarms(rows)
}

func (s *Store) DueAlarmsAfter(clock string, nowOrLess, afterDeadline int64, afterID string, limit int) ([]Alarm, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.Query(
		`SELECT alarm_id, owner_name, clock, deadline, repeat_every, payload
		 FROM alarms WHERE clock = ? AND deadline <= ?
		   AND (deadline > ? OR (deadline = ? AND alarm_id > ?))
		 ORDER BY deadline ASC, alarm_id ASC LIMIT ?`,
		clock, nowOrLess, afterDeadline, afterDeadline, afterID, limit,
	)
	if err != nil {
		return nil, err
	}
	return scanAlarms(rows)
}

func scanAlarms(rows *sql.Rows) ([]Alarm, error) {
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

func (s *Store) DeleteAlarm(alarmID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.w().Exec(`DELETE FROM alarms WHERE alarm_id = ?`, alarmID)
	return err
}

func (s *Store) UpdateAlarmDeadlineCAS(alarmID string, expectedDeadline, newDeadline int64) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	res, err := s.w().Exec(
		`UPDATE alarms SET deadline = ? WHERE alarm_id = ? AND deadline = ?`,
		newDeadline, alarmID, expectedDeadline,
	)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

func (s *Store) DeleteAlarmCAS(alarmID string, expectedDeadline int64) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	res, err := s.w().Exec(
		`DELETE FROM alarms WHERE alarm_id = ? AND deadline = ?`,
		alarmID, expectedDeadline,
	)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}
