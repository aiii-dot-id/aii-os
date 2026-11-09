package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

func (s *Store) LedgerTime(seq uint64) (string, bool, error) {
	if seq == 0 {
		return "", false, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	var ts string
	err := s.db.QueryRow(`SELECT ts FROM ledger WHERE seq = ?`, seq).Scan(&ts)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return ts, true, nil
}

func (s *Store) LedgerTimeAt(seq uint64) (time.Time, bool, error) {
	ts, present, err := s.LedgerTime(seq)
	if err != nil {
		return time.Time{}, false, err
	}
	if !present {
		return time.Time{}, false, nil
	}

	if ts == "" {
		return time.Time{}, false, fmt.Errorf("ledger seq %d: ts is empty", seq)
	}
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return time.Time{}, false, fmt.Errorf("ledger seq %d: ts %q: %w", seq, ts, err)
	}
	return t.UTC(), true, nil
}
