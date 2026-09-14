package store

import (
	"database/sql"
	"errors"
	"fmt"
)

// .
// .
// .
// .
// .
// .

// .
// .
func (s *Store) FindExperienceByContent(content string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var id string
	err := s.db.QueryRow(`SELECT id FROM experiences WHERE content = ? LIMIT 1`, content).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		// .
		// .
		// .
		// .
		// .
		return "", fmt.Errorf("look up duplicate experience: %w", err)
	}
	return id, nil
}

// .
// .
func (s *Store) FindBeliefByStatement(statement, excludeID string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var id string
	err := s.db.QueryRow(
		`SELECT id FROM beliefs WHERE statement = ? AND id != ? AND archived = 0 LIMIT 1`,
		statement, excludeID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("look up duplicate belief: %w", err)
	}
	return id, nil
}

// .
type LedgerEventRow struct {
	Seq       uint64
	Type      string
	Ring      int
	Timestamp string
	Payload   string
}

// .
// .
// .
func (s *Store) SearchLedgerMirror(q string, beforeSeq uint64, limit int) ([]LedgerEventRow, int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var total int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM ledger`).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.db.Query(
		`SELECT seq, type, COALESCE(ring, -1), ts, substr(payload, 1, 200)
		 FROM ledger
		 WHERE seq < ?
		   AND (? = '' OR INSTR(LOWER(type), LOWER(?)) > 0 OR INSTR(LOWER(payload), LOWER(?)) > 0)
		 ORDER BY seq DESC LIMIT ?`,
		beforeSeq, q, q, q, limit)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []LedgerEventRow
	for rows.Next() {
		var r LedgerEventRow
		if err := rows.Scan(&r.Seq, &r.Type, &r.Ring, &r.Timestamp, &r.Payload); err != nil {
			return nil, 0, err
		}
		out = append(out, r)
	}
	return out, total, rows.Err()
}
