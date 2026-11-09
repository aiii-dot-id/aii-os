package store

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/aiii-dot-id/aii-os/internal/store/rows"
)

type TrailBelief struct {
	rows.Belief
	Archived     bool
	SupersededBy string
}

type TrailEdge struct {
	rows.Edge
	Strength float64
}

func (s *Store) BeliefForTrail(id string) (*TrailBelief, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var b TrailBelief
	var archived int
	var superseded sql.NullString
	err := s.db.QueryRow(`SELECT id, statement, ring, COALESCE(node_type, ''), confidence,
			evidence_count, archived, first_seq, last_seq, superseded_by
		 FROM beliefs WHERE id = ?`, id).
		Scan(&b.ID, &b.Statement, &b.Ring, &b.NodeType, &b.Confidence,
			&b.EvidenceCount, &archived, &b.FirstSeq, &b.LastSeq, &superseded)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("belief %q: %w", id, ErrBeliefNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("read belief %q: %w", id, err)
	}
	b.Archived = archived != 0
	if superseded.Valid {
		b.SupersededBy = superseded.String
	}
	return &b, nil
}

func (s *Store) EdgesForTrail(id string, limit int) ([]TrailEdge, error) {
	if limit <= 0 {
		return nil, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	rs, err := s.db.Query(`SELECT id, from_id, to_id, edge_type, strength, created_seq
		 FROM edges WHERE (from_id = ? OR to_id = ?) AND archived = 0
		 ORDER BY created_seq, id LIMIT ?`, id, id, limit)
	if err != nil {
		return nil, fmt.Errorf("read edges for belief %q: %w", id, err)
	}
	defer rs.Close()

	var out []TrailEdge
	for rs.Next() {
		var e TrailEdge
		if err := rs.Scan(&e.ID, &e.FromID, &e.ToID, &e.EdgeType, &e.Strength, &e.CreatedSeq); err != nil {
			return nil, fmt.Errorf("scan edge for belief %q: %w", id, err)
		}
		out = append(out, e)
	}
	if err := rs.Err(); err != nil {
		return nil, fmt.Errorf("read edges for belief %q: %w", id, err)
	}
	return out, nil
}
