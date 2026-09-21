package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// .

// .
type Experience struct {
	ID         string
	Content    string
	Category   string
	Raw        int
	Private    int
	Provenance string
	CreatedSeq uint64
	CreatedAt  string
}

// .
func (s *Store) ListExperiences(n int) ([]Experience, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.Query(
		`SELECT id, content, category, raw, private, provenance, created_seq, created_at
		 FROM experiences ORDER BY created_seq DESC LIMIT ?`, n,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanExperiences(rows)
}

// .
// .
func (s *Store) UnprocessedExperienceCount() (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var count int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM experiences WHERE raw = 1`).Scan(&count)
	return count, err
}

// .
// .
// .
// .
// .
// .
// .
// .
// .
func (s *Store) MarkExperiencesProcessed(ids []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, id := range ids {
		if _, err := s.db.Exec(`UPDATE experiences SET raw = 0 WHERE id = ?`, id); err != nil {
			return err
		}
	}
	return nil
}

func scanExperiences(rows *sql.Rows) ([]Experience, error) {
	var experiences []Experience
	for rows.Next() {
		var e Experience
		var category, provenance sql.NullString
		if err := rows.Scan(&e.ID, &e.Content, &category, &e.Raw, &e.Private, &provenance, &e.CreatedSeq, &e.CreatedAt); err != nil {
			return nil, err
		}
		e.Category = category.String
		e.Provenance = provenance.String
		experiences = append(experiences, e)
	}
	return experiences, rows.Err()
}

// .
// .
// .
func (s *Store) ProvenanceByIDs(ids []string) (map[string]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]string, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	// .
	for _, id := range ids {
		var prov string
		err := s.db.QueryRow(`SELECT provenance FROM experiences WHERE id = ?`, id).Scan(&prov)
		if err == nil {
			out[id] = prov
			continue
		}
		// .
		// .
		// .
		// .
		if !errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("provenance of %s: %w", id, err)
		}
	}
	return out, nil
}

// .
// .
// .
// .
// .
func (s *Store) ListRawExperiences(n int) ([]Experience, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rows, err := s.db.Query(
		`SELECT id, content, category, raw, private, provenance, created_seq, created_at
		 FROM experiences WHERE raw = 1 ORDER BY created_seq ASC LIMIT ?`, n,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanExperiences(rows)
}

// .
// .
// .
// .
// .
// .
const OutcomeObservationPrefix = "exp_outcome_"

// .
// .
// .
// .
// .
func (s *Store) ListRawExperiencesExcept(n int, idPrefix string) ([]Experience, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rows, err := s.db.Query(
		`SELECT id, content, category, raw, private, provenance, created_seq, created_at
		 FROM experiences WHERE raw = 1 AND substr(id, 1, ?) != ? ORDER BY created_seq ASC LIMIT ?`,
		len(idPrefix), idPrefix, n,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanExperiences(rows)
}

// .
// .
// .
func (s *Store) ListExperiencesBefore(n int, beforeSeq uint64) ([]Experience, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rows, err := s.db.Query(
		`SELECT id, content, category, raw, private, provenance, created_seq, created_at
		 FROM experiences WHERE created_seq < ? ORDER BY created_seq DESC LIMIT ?`, beforeSeq, n,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanExperiences(rows)
}

// .
// .
// .
// .
// .
// .
// .
// .
// .
func (s *Store) SearchExperiencesBefore(n int, beforeSeq uint64, match string) ([]Experience, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	// .
	// .
	// .
	// .
	// .
	// .
	q := `SELECT id, content, category, raw, private, provenance, created_seq, created_at
	      FROM experiences WHERE private = 0 AND created_seq < ?`
	args := []interface{}{beforeSeq}
	if match != "" {
		// .
		// .
		q += ` AND LOWER(content) LIKE '%' || LOWER(?) || '%'`
		args = append(args, match)
	}
	q += ` ORDER BY created_seq DESC LIMIT ?`
	args = append(args, n)
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanExperiences(rows)
}

// .
// .
// .
// .
// .
// .
// .
// .
func (s *Store) ListExperiencesSince(after time.Time, n int) ([]Experience, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rows, err := s.db.Query(
		`SELECT id, content, category, raw, private, provenance, created_seq, created_at
		 FROM experiences WHERE private = 0 ORDER BY created_seq DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Experience
	for rows.Next() {
		var e Experience
		var category, provenance sql.NullString
		if err := rows.Scan(&e.ID, &e.Content, &category, &e.Raw, &e.Private, &provenance, &e.CreatedSeq, &e.CreatedAt); err != nil {
			return nil, err
		}
		at, err := time.Parse(time.RFC3339Nano, e.CreatedAt)
		if err != nil {
			return nil, fmt.Errorf("experience %s: created_at %q: %w", e.ID, e.CreatedAt, err)
		}
		if at.Before(after) {
			break
		}
		if len(out) == n {
			break
		}
		e.Category, e.Provenance = category.String, provenance.String
		out = append(out, e)
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
func (s *Store) ListExperiencesByProvenance(provenances []string, n int) ([]Experience, error) {
	if len(provenances) == 0 || n <= 0 {
		return nil, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	marks := strings.TrimSuffix(strings.Repeat("?,", len(provenances)), ",")
	args := make([]interface{}, 0, len(provenances)+1)
	for _, p := range provenances {
		args = append(args, p)
	}
	args = append(args, n)
	rows, err := s.db.Query(
		`SELECT id, content, category, raw, private, provenance, created_seq, created_at
		 FROM experiences WHERE private = 0 AND provenance IN (`+marks+`)
		 ORDER BY created_seq DESC LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanExperiences(rows)
}
