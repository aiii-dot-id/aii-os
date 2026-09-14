package store

import (
	"database/sql"
)

// .

// .
type Intention struct {
	ID        string
	Statement string
	State     string
	// .
	// .
	// .
	Outcome    string
	CreatedSeq uint64
	UpdatedSeq uint64
}

// .
// .
// .
// .
// .
func (s *Store) ListIntentions() ([]Intention, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.Query(
		`SELECT id, statement, state, COALESCE(outcome, ''), created_seq, updated_seq
		 FROM intentions
		 ORDER BY CASE state WHEN 'active' THEN 0 ELSE 1 END, updated_seq DESC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var intentions []Intention
	for rows.Next() {
		var i Intention
		if err := rows.Scan(&i.ID, &i.Statement, &i.State, &i.Outcome, &i.CreatedSeq, &i.UpdatedSeq); err != nil {
			return nil, err
		}
		intentions = append(intentions, i)
	}
	return intentions, rows.Err()
}

type Commitment struct {
	ID            string
	Description   string
	CounterpartID string
	State         string
	Result        string
	RepairState   string
	CreatedSeq    uint64
	UpdatedSeq    uint64
}

// .
// .
func (s *Store) ListCommitments(activeOnly bool) ([]Commitment, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	q := `SELECT id, description, counterpart_id, state, result, repair_state, created_seq, updated_seq
	      FROM commitments`
	if activeOnly {
		q += ` WHERE state IN ('promised','in_progress')`
	}
	q += ` ORDER BY created_seq DESC`
	rows, err := s.db.Query(q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Commitment
	for rows.Next() {
		var c Commitment
		var result, repair sql.NullString
		var updated sql.NullInt64
		if err := rows.Scan(&c.ID, &c.Description, &c.CounterpartID, &c.State, &result, &repair, &c.CreatedSeq, &updated); err != nil {
			return nil, err
		}
		c.Result = result.String
		c.RepairState = repair.String
		if updated.Valid {
			c.UpdatedSeq = uint64(updated.Int64)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// .
// .
type StaleIntention struct {
	ID         string
	Statement  string
	Gap        uint64
	UpdatedSeq uint64
}

// .
// .
// .
// .
// .
// .
func (s *Store) StaleActiveIntentions(minGap uint64) ([]StaleIntention, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rows, err := s.db.Query(
		`SELECT id, statement, (SELECT COALESCE(MAX(seq),0) FROM ledger) - updated_seq AS gap, updated_seq
		 FROM intentions
		 WHERE state = 'active' AND gap >= ?
		 ORDER BY gap DESC`, minGap)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []StaleIntention
	for rows.Next() {
		var si StaleIntention
		if err := rows.Scan(&si.ID, &si.Statement, &si.Gap, &si.UpdatedSeq); err != nil {
			return nil, err
		}
		out = append(out, si)
	}
	return out, rows.Err()
}

// .
// .
// .
// .
// .
// .
// .
// .
func (s *Store) VerdictCounts() (served, partial, unserved int, err error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	err = s.db.QueryRow(`SELECT
		COALESCE(SUM(CASE WHEN outcome LIKE 'served:%' THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN outcome LIKE 'partial:%' THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN outcome LIKE 'unserved:%' THEN 1 ELSE 0 END), 0)
		FROM intentions WHERE outcome IS NOT NULL AND outcome != ''`).
		Scan(&served, &partial, &unserved)
	return
}

// .
// .
// .
// .
// .
// .
// .
func (s *Store) ActivePriorities() ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.activePrioritiesLocked()
}

func (s *Store) activePrioritiesLocked() ([]string, error) {
	rows, err := s.db.Query(`SELECT statement FROM intentions WHERE state = 'active' ORDER BY updated_seq DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var statement string
		if err := rows.Scan(&statement); err != nil {
			return nil, err
		}
		out = append(out, statement)
	}
	return out, rows.Err()
}
