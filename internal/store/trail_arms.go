package store

import (
	"fmt"
	"strings"
)

type TrailArms struct {
	Origins      []TrailEdge
	Contradicts  []TrailEdge
	Supersedes   []TrailEdge
	SupersededBy []TrailEdge
}

var trailOriginTypes = []string{"SUPPORTS", "REINFORCED_BY", "DERIVED_FROM", "SHAPED_BY", "INTERPRETS"}

var trailOriginPlaceholders = quotedTypes(trailOriginTypes)

func quotedTypes(types []string) string {
	quoted := make([]string, len(types))
	for i, t := range types {
		quoted[i] = "'" + t + "'"
	}
	return strings.Join(quoted, ",")
}

func (s *Store) TrailArmsForBelief(id string, perArm int) (*TrailArms, error) {
	arms := &TrailArms{}
	if perArm <= 0 {
		return arms, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	var err error
	if arms.Origins, err = s.trailEdgesLocked(`SELECT id, from_id, to_id, edge_type, strength, created_seq
		 FROM edges WHERE to_id = ? AND from_id <> ? AND archived = 0
		   AND edge_type IN (`+trailOriginPlaceholders+`)
		 ORDER BY created_seq, id LIMIT ?`, id, id, perArm+1); err != nil {
		return nil, err
	}
	if arms.Contradicts, err = s.trailEdgesLocked(`SELECT id, from_id, to_id, edge_type, strength, created_seq
		 FROM edges WHERE to_id = ? AND from_id <> ? AND archived = 0 AND edge_type = 'CONTRADICTS'
		 ORDER BY created_seq, id LIMIT ?`, id, id, perArm+1); err != nil {
		return nil, err
	}
	if arms.Supersedes, err = s.trailEdgesLocked(`SELECT id, from_id, to_id, edge_type, strength, created_seq
		 FROM edges WHERE from_id = ? AND archived = 0 AND edge_type = 'SUPERSEDES'
		 ORDER BY created_seq, id LIMIT ?`, id, perArm+1); err != nil {
		return nil, err
	}
	if arms.SupersededBy, err = s.trailEdgesLocked(`SELECT id, from_id, to_id, edge_type, strength, created_seq
		 FROM edges WHERE to_id = ? AND from_id <> ? AND archived = 0 AND edge_type = 'SUPERSEDES'
		 ORDER BY created_seq, id LIMIT ?`, id, id, perArm+1); err != nil {
		return nil, err
	}
	return arms, nil
}

func (s *Store) trailEdgesLocked(query string, args ...any) ([]TrailEdge, error) {
	rs, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("read trail edges: %w", err)
	}
	defer rs.Close()

	var out []TrailEdge
	for rs.Next() {
		var e TrailEdge
		if err := rs.Scan(&e.ID, &e.FromID, &e.ToID, &e.EdgeType, &e.Strength, &e.CreatedSeq); err != nil {
			return nil, fmt.Errorf("scan trail edge: %w", err)
		}
		out = append(out, e)
	}
	if err := rs.Err(); err != nil {
		return nil, fmt.Errorf("read trail edges: %w", err)
	}
	return out, nil
}
