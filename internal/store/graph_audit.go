package store

import (
	"fmt"
	"strings"
)

// .
// .
// .
// .
// .
// .

// .
type GraphAudit struct {
	// .
	// .
	// .
	// .
	Ungrounded       int
	UngroundedSample []string
	// .
	// .
	// .
	Orphans       int
	OrphansSample []string
	// .
	// .
	Tensions int
}

// .
// .
const entityUnion = `SELECT id FROM beliefs UNION ALL SELECT id FROM experiences UNION ALL SELECT id FROM intentions
	UNION ALL SELECT id FROM commitments UNION ALL SELECT id FROM relationships UNION ALL SELECT id FROM self_model_synthesis`

// .
func (s *Store) GraphAudit() (GraphAudit, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var a GraphAudit
	const ungrounded = `FROM beliefs b WHERE b.archived = 0 AND b.superseded_by IS NULL AND NOT EXISTS (
		SELECT 1 FROM edges e WHERE e.archived = 0 AND e.edge_type IN ('SUPPORTS', 'DERIVED_FROM', 'REINFORCED_BY')
		AND (e.from_id = b.id OR e.to_id = b.id))`
	if err := s.db.QueryRow("SELECT COUNT(*) " + ungrounded).Scan(&a.Ungrounded); err != nil {
		return a, fmt.Errorf("ungrounded beliefs: %w", err)
	}
	sample, err := s.idSample("SELECT b.id " + ungrounded + " ORDER BY b.first_seq LIMIT 5")
	if err != nil {
		return a, err
	}
	a.UngroundedSample = sample

	orphans := `FROM edges e WHERE e.archived = 0 AND (
		NOT EXISTS (SELECT 1 FROM (` + entityUnion + `) x WHERE x.id = e.from_id)
		OR NOT EXISTS (SELECT 1 FROM (` + entityUnion + `) y WHERE y.id = e.to_id))`
	if err := s.db.QueryRow("SELECT COUNT(*) " + orphans).Scan(&a.Orphans); err != nil {
		return a, fmt.Errorf("orphan edges: %w", err)
	}
	sample, err = s.idSample("SELECT e.id " + orphans + " ORDER BY e.created_seq LIMIT 5")
	if err != nil {
		return a, err
	}
	a.OrphansSample = sample

	if err := s.db.QueryRow(`SELECT COUNT(*) FROM edges WHERE edge_type = 'CONTRADICTS' AND archived = 0`).Scan(&a.Tensions); err != nil {
		return a, fmt.Errorf("tensions: %w", err)
	}
	return a, nil
}

func (s *Store) idSample(query string) ([]string, error) {
	rows, err := s.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// .
// .
func RenderGraphAudit(a GraphAudit) string {
	sample := func(ids []string) string {
		if len(ids) == 0 {
			return ""
		}
		return " (e.g. " + strings.Join(ids, ", ") + ")"
	}
	return fmt.Sprintf("Record audit — the graph read against its own rules (observational):\n"+
		"  ungrounded beliefs: %d — current beliefs no evidence edge touches; new ones are refused (R99), so these predate it or lost their evidence%s\n"+
		"  orphan edges: %d — live edges with an endpoint no entity holds; never counted for standing%s\n"+
		"  open tensions: %d — live CONTRADICTS edges, the record's tension registry",
		a.Ungrounded, sample(a.UngroundedSample), a.Orphans, sample(a.OrphansSample), a.Tensions)
}
