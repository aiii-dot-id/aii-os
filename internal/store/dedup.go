package store

import (
	"database/sql"
	"errors"
	"fmt"
	"math"
	"unicode/utf8"

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

var ErrNotInMirror = errors.New("no such record in the database's copy of the record")

type LedgerMirrorRow struct {
	Seq       uint64
	Timestamp string
	Type      string

	Ring         int
	PayloadBytes int64
	Head         []byte
}

func mirrorSeqCeiling(seq uint64) uint64 {
	if seq == 0 || seq > math.MaxInt64 {
		return math.MaxInt64
	}
	return seq
}

func (s *Store) LedgerMirrorPage(beforeSeq uint64, typ, words string, limit, headBytes int) ([]LedgerMirrorRow, error) {
	if limit <= 0 {
		return nil, nil
	}
	if headBytes < 0 {
		headBytes = 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	rows, err := s.db.Query(
		`SELECT seq, ts, type, ring, length(CAST(payload AS BLOB)), substr(CAST(payload AS BLOB), 1, ?)
		 FROM ledger
		 WHERE seq < ?
		   AND (? = '' OR type = ?)
		   AND (? = '' OR INSTR(LOWER(type), LOWER(?)) > 0 OR INSTR(LOWER(payload), LOWER(?)) > 0)
		 ORDER BY seq DESC LIMIT ?`,
		headBytes, mirrorSeqCeiling(beforeSeq), typ, typ, words, words, words, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LedgerMirrorRow
	for rows.Next() {
		var r LedgerMirrorRow
		var ring sql.NullInt64
		if err := rows.Scan(&r.Seq, &r.Timestamp, &r.Type, &ring, &r.PayloadBytes, &r.Head); err != nil {
			return nil, err
		}
		r.Ring = -1
		if ring.Valid {
			r.Ring = int(ring.Int64)
		}
		if int64(len(r.Head)) < r.PayloadBytes {
			r.Head = wholeCharacters(r.Head)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func wholeCharacters(b []byte) []byte {
	start := len(b) - 1
	for start > 0 && len(b)-start < utf8.UTFMax && !utf8.RuneStart(b[start]) {
		start--
	}
	if start >= 0 && !utf8.FullRune(b[start:]) {
		return b[:start]
	}
	return b
}

func (s *Store) LedgerMirrorPayload(seq uint64, offset, length int64) ([]byte, int64, error) {
	if offset < 0 || length < 0 {
		return nil, 0, fmt.Errorf("a payload range starts and runs at zero or more bytes (offset %d, length %d)", offset, length)
	}
	if seq > math.MaxInt64 {
		return nil, 0, ErrNotInMirror
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	var total int64
	var part []byte
	err := s.db.QueryRow(
		`SELECT length(CAST(payload AS BLOB)), substr(CAST(payload AS BLOB), ?, ?) FROM ledger WHERE seq = ?`,
		offset+1, length, seq).Scan(&total, &part)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, 0, ErrNotInMirror
	}
	if err != nil {
		return nil, 0, err
	}
	return part, total, nil
}

func (s *Store) LedgerMirrorTypes() ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rows, err := s.db.Query(`SELECT DISTINCT type FROM ledger ORDER BY type`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
