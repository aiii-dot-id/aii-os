package store

import (
	"database/sql"
)

// --- Intentions ---

// Intention is an active goal.
type Intention struct {
	ID        string
	Statement string
	State     string
	// Outcome is the owner's verdict at completion — "served: ...",
	// "partial: ...", or "unserved: ..." (gated at the commit verb since
	// the evaluate layer; empty on intentions completed before it).
	Outcome    string
	CreatedSeq uint64
	UpdatedSeq uint64
}

// ListIntentions returns ALL intentions, active first (then most recently
// updated). Callers with an active-only interest filter on State — the
// completed/abandoned history is identity state, not noise (the dashboard
// renders it struck-through). Fixed 2026-08-16: it previously returned
// active-only, silently hiding finished work from every reader.
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

// ListCommitments returns commitments by state filter ("active" =
// promised/in_progress), newest first.
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

// StaleIntention is one active intention the world has moved past: no
// state change for at least minGap ledger events.
type StaleIntention struct {
	ID        string
	Statement string
	Gap       uint64 // current ledger seq - updated_seq
}

// StaleActiveIntentions lists active intentions whose last touch is at
// least minGap events behind the ledger head, most stale first. The gap
// is measured in EVENTS, not wall time, deliberately: an identity that
// lived a hundred events past an intention without touching it has
// drifted from it regardless of the calendar, and an identity that was
// simply off has drifted from nothing.
func (s *Store) StaleActiveIntentions(minGap uint64) ([]StaleIntention, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rows, err := s.db.Query(
		`SELECT id, statement, (SELECT COALESCE(MAX(seq),0) FROM ledger) - updated_seq AS gap
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
		if err := rows.Scan(&si.ID, &si.Statement, &si.Gap); err != nil {
			return nil, err
		}
		out = append(out, si)
	}
	return out, rows.Err()
}

// VerdictCounts tallies the identity's CLAIMED completion outcomes —
// the served:/partial:/unserved: prefixes the yardstick gate requires
// on intention.state_change. CLAIMS, not verified results: the peer
// record measured 73.8% of self-reported optimization wins as proxy
// gains (reward-hacking literature, 2026-08-26 review), so every
// surface showing these numbers says "self-reported". Verifying them
// against reality is evaluate stage 2's job; counting them honestly
// is this one's.
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
