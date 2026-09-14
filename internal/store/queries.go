package store

import (
	"database/sql"
	"fmt"

	"time"
)

// .

// .
type Stats struct {
	BeliefCount       int
	ReflectionCount   int
	ExperienceCount   int
	ConversationCount int
	IntentionCount    int
	WorkSessionCount  int
	LedgerSeq         uint64
	LifetimeTicks     int64
}

// .
func (s *Store) GetStats() (*Stats, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	stats := &Stats{}

	// .
	// .
	// .
	// .
	var firstErr error
	count := func(dest any, query string) {
		if firstErr != nil {
			return
		}
		// .
		// .
		if err := s.db.QueryRow(query).Scan(dest); err != nil {
			firstErr = fmt.Errorf("stats: %s: %w", query, err)
		}
	}

	// .
	// .
	// .
	// .
	count(&stats.BeliefCount, `SELECT COUNT(*) FROM beliefs WHERE archived = 0 AND superseded_by IS NULL`)
	count(&stats.ReflectionCount, `SELECT COUNT(*) FROM self_model_synthesis`)
	count(&stats.ExperienceCount, `SELECT COUNT(*) FROM experiences`)
	count(&stats.ConversationCount, `SELECT COUNT(*) FROM conversations`)
	count(&stats.IntentionCount, `SELECT COUNT(*) FROM intentions WHERE state = 'active'`)
	count(&stats.WorkSessionCount, `SELECT COUNT(*) FROM work_sessions`)
	count(&stats.LedgerSeq, `SELECT COALESCE(MAX(seq), 0) FROM ledger`)
	if firstErr != nil {
		return nil, firstErr
	}

	// .
	// .
	// .
	// .
	var ticks sql.NullInt64
	if err := s.db.QueryRow(`SELECT COALESCE(lifetime_ticks, 0) FROM identity_lifetime WHERE singleton_id = 'current'`).Scan(&ticks); err != nil && err != sql.ErrNoRows {
		return nil, fmt.Errorf("stats: lifetime ticks: %w", err)
	}
	if ticks.Valid {
		stats.LifetimeTicks = ticks.Int64
	}

	return stats, nil
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
func (s *Store) EntityExists(id string) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, table := range []string{"beliefs", "experiences", "intentions", "commitments", "relationships", "self_model_synthesis"} {
		var one int
		err := s.db.QueryRow("SELECT 1 FROM "+table+" WHERE id = ?", id).Scan(&one)
		if err == nil {
			return true, nil
		}
		if err != sql.ErrNoRows {
			return false, err
		}
	}
	return false, nil
}

// .

// .
func (s *Store) SaveWitnessEnvelope(canonicalJSON []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(
		`INSERT INTO witness_identity (singleton_id, envelope_json, created_at) VALUES (1, ?, ?)
		 ON CONFLICT(singleton_id) DO NOTHING`,
		string(canonicalJSON), time.Now().UTC().Format(time.RFC3339Nano),
	)
	return err
}

// .
func (s *Store) LoadWitnessEnvelope() ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var env string
	err := s.db.QueryRow(`SELECT envelope_json FROM witness_identity WHERE singleton_id = 1`).Scan(&env)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return []byte(env), nil
}

// .
// .
func (s *Store) QueryRowForTest(query string, args ...interface{}) interface {
	Scan(dest ...interface{}) error
} {
	return s.db.QueryRow(query, args...)
}
