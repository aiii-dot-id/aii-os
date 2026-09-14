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
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .

// .
// .
// .
// .
// .
// .
// .
// .
// .
func (s *Store) StandingFor(id string) (string, error) {
	edges, err := s.ListEdgesForBelief(id)
	if err != nil {
		return "", fmt.Errorf("standing of %s: edges: %w", id, err)
	}

	var confirmedAt int64
	s.mu.RLock()
	err = s.db.QueryRow(`SELECT confirmed_at_ticks FROM beliefs WHERE id = ?`, id).Scan(&confirmedAt)
	s.mu.RUnlock()
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("standing of %s: confirmed_at: %w", id, err)
	}

	hasContradiction := false
	sources := map[string]bool{}
	var fromIDs []string
	for _, e := range edges {
		if e.EdgeType == "CONTRADICTS" {
			hasContradiction = true
			continue
		}
		if e.EdgeType == "SUPPORTS" || e.EdgeType == "REINFORCED_BY" || e.EdgeType == "DERIVED_FROM" {
			// .
			// .
			// .
			// .
			// .
			// .
			// .
			exists, err := s.EntityExists(e.FromID)
			if err != nil {
				return "", fmt.Errorf("standing of %s: cannot verify evidence %s exists: %w", id, e.FromID, err)
			}
			if !exists {
				continue
			}
			sources[e.FromID] = true
			fromIDs = append(fromIDs, e.FromID)
		}
	}

	if hasContradiction {
		return "suspect", nil
	}
	if len(sources) >= 3 {
		classes, err := s.authorshipClasses(fromIDs)
		if err != nil {
			return "", fmt.Errorf("standing of %s: %w", id, err)
		}
		if classes >= 2 {
			var ticks int64
			s.mu.RLock()
			err = s.db.QueryRow(`SELECT lifetime_ticks FROM identity_lifetime WHERE singleton_id = 'current'`).Scan(&ticks)
			s.mu.RUnlock()
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return "", fmt.Errorf("standing of %s: lifetime ticks: %w", id, err)
			}
			if confirmedAt > 0 && ticks-confirmedAt >= 50 {
				return "trusted", nil
			}
			return "confirmed", nil
		}
	}
	return "new", nil
}

// .
// .
// .
func authorshipClassOf(provenance string) string {
	switch provenance {
	case "operator", "external":
		return provenance
	default:
		return "resident"
	}
}

func (s *Store) authorshipClasses(fromIDs []string) (int, error) {
	prov, err := s.ProvenanceByIDs(fromIDs)
	if err != nil {
		return 0, fmt.Errorf("authorship classes: %w", err)
	}
	classes := map[string]bool{}
	for _, id := range fromIDs {
		classes[authorshipClassOf(prov[id])] = true
	}
	return len(classes), nil
}

// .
// .
// .
// .
type TensionPair struct {
	LeftID, RightID string
	EdgeID          string
}

func (s *Store) TensionsView() ([]TensionPair, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rows, err := s.db.Query(
		`SELECT id, from_id, to_id FROM edges
		 WHERE edge_type = 'CONTRADICTS' AND archived = 0
		 ORDER BY created_seq ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TensionPair
	for rows.Next() {
		var tp TensionPair
		if err := rows.Scan(&tp.EdgeID, &tp.LeftID, &tp.RightID); err != nil {
			return nil, err
		}
		out = append(out, tp)
	}
	return out, rows.Err()
}

// .
// .
func (s *Store) StatementsFor(ids []string) (map[string]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]string, len(ids))
	for _, id := range ids {
		var stmt string
		err := s.db.QueryRow(`SELECT statement FROM beliefs WHERE id = ? AND archived = 0`, id).Scan(&stmt)
		if err == nil {
			out[id] = stmt
		}
		// .
	}
	return out, nil
}
