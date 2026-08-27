package store

import (
	"database/sql"
)

// --- Beliefs ---

// Belief is a projection of a belief.upsert or belief.promote event.
type Belief struct {
	ID               string
	Statement        string
	Ring             int
	NodeType         string
	Confidence       float64
	EvidenceCount    int
	FirstSeq         uint64
	LastSeq          uint64
	ConfirmedAtTicks int64 // Life-clock ticks when belief was confirmed (0 if never)
}

// ListBeliefs returns all beliefs, ordered by ring then confidence.
func (s *Store) ListBeliefs() ([]Belief, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.Query(
		`SELECT id, statement, ring, COALESCE(node_type, ''), confidence, evidence_count, confirmed_at_ticks, first_seq, last_seq
		 FROM beliefs
		 WHERE archived = 0 AND superseded_by IS NULL
		 ORDER BY ring ASC, confidence DESC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanBeliefs(rows)
}

// GetBelief returns a single belief by ID.
func (s *Store) GetBelief(id string) (*Belief, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var b Belief
	err := s.db.QueryRow(
		`SELECT id, statement, ring, COALESCE(node_type, ''), confidence, evidence_count, confirmed_at_ticks, first_seq, last_seq
		 FROM beliefs WHERE id = ?`, id,
	).Scan(&b.ID, &b.Statement, &b.Ring, &b.NodeType, &b.Confidence, &b.EvidenceCount, &b.ConfirmedAtTicks, &b.FirstSeq, &b.LastSeq)
	if err != nil {
		return nil, err
	}
	return &b, nil
}

func scanBeliefs(rows *sql.Rows) ([]Belief, error) {
	var beliefs []Belief
	for rows.Next() {
		var b Belief
		if err := rows.Scan(&b.ID, &b.Statement, &b.Ring, &b.NodeType, &b.Confidence, &b.EvidenceCount, &b.ConfirmedAtTicks, &b.FirstSeq, &b.LastSeq); err != nil {
			return nil, err
		}
		beliefs = append(beliefs, b)
	}
	return beliefs, rows.Err()
}

// --- Edges (for R16 ladder) ---

// Edge is a provenance graph edge.
type Edge struct {
	ID         string
	FromID     string
	ToID       string
	EdgeType   string
	CreatedSeq uint64
}

// ListEdgesForBelief returns all edges connected to a belief.
func (s *Store) ListEdgesForBelief(beliefID string) ([]Edge, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.Query(
		`SELECT id, from_id, to_id, edge_type, created_seq
		 FROM edges WHERE (from_id = ? OR to_id = ?) AND archived = 0`,
		beliefID, beliefID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var edges []Edge
	for rows.Next() {
		var e Edge
		if err := rows.Scan(&e.ID, &e.FromID, &e.ToID, &e.EdgeType, &e.CreatedSeq); err != nil {
			return nil, err
		}
		edges = append(edges, e)
	}
	return edges, rows.Err()
}

// StaleBelief is the probe-nominator's pick: a ring-3 plain belief
// whose last ledger touch is oldest against the moving record.
type StaleBelief struct {
	ID        string
	Statement string
	Gap       uint64
}

// OldestStaleBelief returns the active ring-3 PLAIN belief (no
// node_type — values and working_style have their own lifecycles)
// that has gone longest untouched, with its gap, or ok=false when
// none reaches minGap. The same moving-ledger arithmetic as
// StaleActiveIntentions: staleness is distance from the record's
// head, not wall time.
func (s *Store) OldestStaleBelief(minGap uint64) (StaleBelief, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var sb StaleBelief
	err := s.db.QueryRow(
		`SELECT id, statement, (SELECT COALESCE(MAX(seq),0) FROM ledger) - last_seq AS gap
		 FROM beliefs
		 WHERE archived = 0 AND superseded_by IS NULL AND ring = 3 AND node_type IS NULL AND gap >= ?
		 ORDER BY gap DESC LIMIT 1`, minGap).Scan(&sb.ID, &sb.Statement, &sb.Gap)
	if err == sql.ErrNoRows {
		return StaleBelief{}, false, nil
	}
	if err != nil {
		return StaleBelief{}, false, err
	}
	return sb, true, nil
}
