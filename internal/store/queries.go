package store

import (
	"database/sql"

	"time"
)

// --- Stats ---

// Stats holds summary counts for the dashboard status view.
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

// GetStats returns summary statistics.
func (s *Store) GetStats() (*Stats, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	stats := &Stats{}

	// Belief count must match ListBeliefs' live filter (archived = 0,
	// superseded_by IS NULL) — the status view and the identity view
	// must agree by construction. COUNT(*) counts the graveyard too;
	// that disagreement was the "belief-count anomaly" (2026-08-22).
	s.db.QueryRow(`SELECT COUNT(*) FROM beliefs WHERE archived = 0 AND superseded_by IS NULL`).Scan(&stats.BeliefCount)
	s.db.QueryRow(`SELECT COUNT(*) FROM self_model_synthesis`).Scan(&stats.ReflectionCount)
	s.db.QueryRow(`SELECT COUNT(*) FROM experiences`).Scan(&stats.ExperienceCount)
	s.db.QueryRow(`SELECT COUNT(*) FROM conversations`).Scan(&stats.ConversationCount)
	s.db.QueryRow(`SELECT COUNT(*) FROM intentions WHERE state = 'active'`).Scan(&stats.IntentionCount)
	s.db.QueryRow(`SELECT COUNT(*) FROM work_sessions`).Scan(&stats.WorkSessionCount)
	s.db.QueryRow(`SELECT COALESCE(MAX(seq), 0) FROM ledger`).Scan(&stats.LedgerSeq)
	var ticks sql.NullInt64
	s.db.QueryRow(`SELECT COALESCE(lifetime_ticks, 0) FROM identity_lifetime WHERE singleton_id = 'current'`).Scan(&ticks)
	if ticks.Valid {
		stats.LifetimeTicks = ticks.Int64
	}

	return stats, nil
}

// --- Utility ---

// now returns current UTC time as RFC 3339 string.
func now() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}

// Ensure import is used

func (s *Store) EntityExists(id string) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, table := range []string{"beliefs", "experiences", "intentions", "commitments", "relationships"} {
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

// --- Witness identity envelope (stable across sessions) ---

// SaveWitnessEnvelope persists the identity's canonical witness envelope.
func (s *Store) SaveWitnessEnvelope(canonicalJSON []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(
		`INSERT INTO witness_identity (singleton_id, envelope_json, created_at) VALUES (1, ?, ?)
		 ON CONFLICT(singleton_id) DO NOTHING`, // immutable once set — a changed envelope is a changed identity
		string(canonicalJSON), time.Now().UTC().Format(time.RFC3339Nano),
	)
	return err
}

// LoadWitnessEnvelope returns the persisted canonical envelope, or nil.
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

// QueryRowForTest exposes a single-row read for tests outside the store
// package (the cognitive time tests assert alarm rows directly). Read-only.
func (s *Store) QueryRowForTest(query string, args ...interface{}) interface {
	Scan(dest ...interface{}) error
} {
	return s.db.QueryRow(query, args...)
}
