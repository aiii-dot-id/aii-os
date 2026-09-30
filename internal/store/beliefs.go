package store

import (
	"database/sql"
	"fmt"
)

type Belief struct {
	ID               string
	Statement        string
	Ring             int
	NodeType         string
	Confidence       float64
	EvidenceCount    int
	FirstSeq         uint64
	LastSeq          uint64
	ConfirmedAtTicks int64
}

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

type RetiredBelief struct {
	ID        string
	Statement string

	RetiredAt string

	Replacement TensionEnd
}

const retiredBeliefsSQL = `
WITH retired AS (
  SELECT b.id, b.statement, COALESCE(b.superseded_by, '') AS successor,
         COALESCE((SELECT e.created_seq FROM edges e
                    WHERE e.edge_type = 'SUPERSEDES' AND e.from_id = b.superseded_by AND e.to_id = b.id),
                  b.last_seq) AS seq
    FROM beliefs b
   WHERE b.ring = 3 AND (b.archived = 1 OR b.superseded_by IS NOT NULL))
SELECT r.id, r.statement, r.successor, l.ts, COUNT(*) OVER ()
  FROM retired r JOIN ledger l ON l.seq = r.seq
 WHERE r.seq > ?
 ORDER BY r.seq DESC
 LIMIT ?`

func (s *Store) RetiredBeliefs(after uint64, limit int) ([]RetiredBelief, int, error) {
	if limit < 1 {
		return nil, 0, fmt.Errorf("retired beliefs: a limit of %d shows nothing", limit)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	rows, err := s.db.Query(retiredBeliefsSQL, after, limit)
	if err != nil {
		return nil, 0, fmt.Errorf("retired beliefs: %w", err)
	}
	var out []RetiredBelief
	var successors []string
	total := 0
	for rows.Next() {
		var r RetiredBelief
		var successor string
		if err := rows.Scan(&r.ID, &r.Statement, &successor, &r.RetiredAt, &total); err != nil {
			rows.Close()
			return nil, 0, fmt.Errorf("retired beliefs: %w", err)
		}
		out = append(out, r)
		successors = append(successors, successor)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, 0, fmt.Errorf("retired beliefs: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, 0, fmt.Errorf("retired beliefs: %w", err)
	}
	for i := range out {
		if successors[i] == "" {
			continue
		}
		if out[i].Replacement, err = s.chainEndLocked(out[i].ID, successors[i]); err != nil {
			return nil, 0, fmt.Errorf("retired beliefs: %s: %w", out[i].ID, err)
		}
	}
	return out, total - len(out), nil
}

func (s *Store) chainEndLocked(id, first string) (TensionEnd, error) {
	seen := map[string]bool{id: true}
	end := first
	for {
		seen[end] = true
		var next sql.NullString
		if err := s.db.QueryRow(`SELECT superseded_by FROM beliefs WHERE id = ?`, end).Scan(&next); err != nil {
			return TensionEnd{}, fmt.Errorf("supersession chain at %s: %w", end, err)
		}
		if !next.Valid || seen[next.String] {
			break
		}
		end = next.String
	}
	if end == id {
		return TensionEnd{}, nil
	}
	head, isBelief, err := s.beliefEndLocked(end)
	if err != nil {
		return TensionEnd{}, err
	}
	if !isBelief {
		return TensionEnd{}, fmt.Errorf("supersession chain ends at %s, which is no belief", end)
	}
	return head, nil
}

func (s *Store) GetBelief(id string) (*Belief, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return beliefRead(s.db, id)
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

type Edge struct {
	ID         string
	FromID     string
	ToID       string
	EdgeType   string
	CreatedSeq uint64
}

func (s *Store) ListEdgesForBelief(beliefID string) ([]Edge, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return edgesForBeliefRead(s.db, beliefID)
}

type StaleBelief struct {
	ID        string
	Statement string
	Gap       uint64
}

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
