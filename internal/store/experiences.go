package store

import (
	"database/sql"
)

// --- Experiences ---

// Experience is a raw observation (note verb output).
type Experience struct {
	ID         string
	Content    string
	Category   string
	Raw        int
	Private    int // 1 = private (Charter #9: held, never metabolized)
	Provenance string
	CreatedSeq uint64
	CreatedAt  string
}

// ListExperiences returns recent experiences.
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

// UnprocessedExperienceCount returns the number of raw, unprocessed experiences.
// Used by DREAM/CONSOLIDATE facility predicates (capacity-gated, R29).
func (s *Store) UnprocessedExperienceCount() (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var count int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM experiences WHERE raw = 1`).Scan(&count)
	return count, err
}

// MarkExperiencesProcessed sets raw=0 for the given experience IDs.
//
// NOT the metabolizers' path anymore (external review 2026-08-20, H6):
// DREAM/CONSOLIDATE consume through their run markers
// (consolidation.run / dream.run), whose MATERIALIZER writes raw=0 —
// consumed state is f(ledger) and survives replay. This method remains
// as the store-side mechanism for non-ledger callers (the note verb's
// Charter #9 private seal belt-and-braces); cognition's interfaces no
// longer carry it, so a facility cannot reach it by construction.
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

// ProvenanceByIDs returns id → provenance for the given evidence ids
// (absent ids omitted). The R16 ladder resolves supporting evidence's
// authorship classes through this.
func (s *Store) ProvenanceByIDs(ids []string) (map[string]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]string, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	// small n (ladder walk); one query per id keeps the SQL trivial
	for _, id := range ids {
		var prov string
		err := s.db.QueryRow(`SELECT provenance FROM experiences WHERE id = ?`, id).Scan(&prov)
		if err == nil {
			out[id] = prov
		}
	}
	return out, nil
}

// ListRawExperiences returns up to n unprocessed (raw=1) experiences,
// OLDEST FIRST — the metabolizer's queue. "Most recent N then filter
// raw" starved older raw notes indefinitely under steady note inflow
// (dream/consolidate consumed only what landed in the recent window);
// oldest-first guarantees progress (2026-08-17 review).
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

// ListExperiencesBefore pages experiences older than the given created_seq
// (exclusive), newest-first — the pass-through cursor for recall (R1: the
// source owns the cursor; the footer must be able to tell the truth).
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
