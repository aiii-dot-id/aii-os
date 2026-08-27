package store

// Duplicate mirrors + ledger inspection reads (R60, 2026-08-18): the
// substrate pushes back on exact-duplicate mints — the identity
// overrides deliberately or reinforces instead — and the single read
// verb gains event-level inspection over the ledger mirror. No
// similarity machinery anywhere (R45; semantic sameness is
// CONSOLIDATE's mandate; vector memory is future plugin-plane).

// FindExperienceByContent returns the id of an experience with EXACTLY
// this content ("" = none).
func (s *Store) FindExperienceByContent(content string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var id string
	err := s.db.QueryRow(`SELECT id FROM experiences WHERE content = ? LIMIT 1`, content).Scan(&id)
	if err != nil {
		return "", nil // no rows = no duplicate; other errors fail-soft to no-pushback
	}
	return id, nil
}

// FindBeliefByStatement returns the id of a LIVE belief with exactly
// this statement under a DIFFERENT id ("" = none).
func (s *Store) FindBeliefByStatement(statement, excludeID string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var id string
	err := s.db.QueryRow(
		`SELECT id FROM beliefs WHERE statement = ? AND id != ? AND archived = 0 LIMIT 1`,
		statement, excludeID).Scan(&id)
	if err != nil {
		return "", nil
	}
	return id, nil
}

// LedgerEventRow is one inspection row from the ledger mirror.
type LedgerEventRow struct {
	Seq       uint64
	Type      string
	Ring      int
	Timestamp string
	Payload   string
}

// SearchLedgerMirror reads the store's ledger mirror for inspection
// (recall's Ledger source): substring match over type + payload,
// newest first, seq strictly below beforeSeq, bounded.
func (s *Store) SearchLedgerMirror(q string, beforeSeq uint64, limit int) ([]LedgerEventRow, int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var total int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM ledger`).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.db.Query(
		`SELECT seq, type, COALESCE(ring, -1), timestamp, substr(payload, 1, 200)
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
