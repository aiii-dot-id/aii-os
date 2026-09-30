package store

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
)

func (s *Store) FindExperienceByContent(content string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var id string
	err := s.db.QueryRow(`SELECT id FROM experiences WHERE content = ? LIMIT 1`, content).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {

		return "", fmt.Errorf("look up duplicate experience: %w", err)
	}
	return id, nil
}

func (s *Store) FindBeliefByStatement(statement, excludeID string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var id string
	err := s.db.QueryRow(
		`SELECT id FROM beliefs WHERE statement = ? AND id != ? AND archived = 0 AND superseded_by IS NULL LIMIT 1`,
		statement, excludeID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("look up duplicate belief: %w", err)
	}
	return id, nil
}

type LedgerEventRow struct {
	Seq       uint64
	Type      string
	Ring      int
	Timestamp string
	Payload   string

	Citation      ledger.Citation
	CitationError error
}

func (s *Store) SearchLedgerMirror(q string, beforeSeq uint64, limit int) ([]LedgerEventRow, int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var total int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM ledger`).Scan(&total); err != nil {
		return nil, 0, err
	}
	self, identityErr := s.ownFingerprintLocked()
	rows, err := s.db.Query(
		`SELECT seq, type, ring, ts, substr(payload, 1, 200), prev, content
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
		var ring sql.NullInt64
		var prev, content string
		if err := rows.Scan(&r.Seq, &r.Type, &ring, &r.Timestamp, &r.Payload, &prev, &content); err != nil {
			return nil, 0, err
		}
		r.Ring = -1
		r.Citation = ledger.Citation{Identity: self, Seq: r.Seq}
		r.CitationError = identityErr
		if ring.Valid {
			r.Ring = int(ring.Int64)
			evt := ledger.Event{Seq: r.Seq, Prev: prev, Timestamp: r.Timestamp,
				Type: ledger.EventType(r.Type), Ring: r.Ring, Content: content}
			r.Citation.EntryHash = evt.EntryHash()
		} else {
			r.CitationError = errors.Join(identityErr, fmt.Errorf("record %d has no canonical ring in the mirror", r.Seq))
		}
		out = append(out, r)
	}
	return out, total, rows.Err()
}
