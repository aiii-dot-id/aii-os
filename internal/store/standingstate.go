package store

import (
	"database/sql"
	"errors"
	"time"
)

// .
// .
// .
// .
// .
// .
// .
// .
// .

// .
// .
func (s *Store) StandingState() (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var text string
	err := s.db.QueryRow(`SELECT text FROM standing_state WHERE singleton_id = 'current'`).Scan(&text)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return text, nil
}

// .
// .
// .
func (s *Store) SetStandingState(text string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(
		`INSERT INTO standing_state (singleton_id, text, updated_ms) VALUES ('current', ?, ?)
		 ON CONFLICT(singleton_id) DO UPDATE SET text = excluded.text, updated_ms = excluded.updated_ms`,
		text, time.Now().UTC().UnixMilli())
	return err
}
