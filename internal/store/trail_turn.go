package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

func (s *Store) ExperienceDreamedTurn(id string) (string, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var payload string
	err := s.db.QueryRow(
		`SELECT l.payload
		   FROM experiences x JOIN ledger l ON l.seq = x.created_seq
		  WHERE x.id = ?`, id,
	).Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, ErrExperienceNotFound
	}
	if err != nil {
		return "", false, err
	}

	var holder struct {
		DreamedThrough *struct {
			Turn *string `json:"turn"`
		} `json:"dreamed_through"`
	}
	if err := json.Unmarshal([]byte(payload), &holder); err != nil {
		return "", false, fmt.Errorf("experience %s: payload: %w", id, err)
	}
	if holder.DreamedThrough == nil || holder.DreamedThrough.Turn == nil {
		return "", false, nil
	}

	if *holder.DreamedThrough.Turn == "" {
		return "", false, fmt.Errorf("experience %s: dreamed_through.turn is empty", id)
	}
	return *holder.DreamedThrough.Turn, true, nil
}
