package store

import (
	"database/sql"
	"encoding/json"
)

// --- Relationships ---

// Relationship is a projected relationship from the ledger.
type Relationship struct {
	ID               string
	CounterpartName  string
	CounterpartRole  string
	TrustLevel       string
	AutonomyLevel    string
	RelationshipType string
	CharterText      string
	CreatedSeq       uint64
	UpdatedSeq       uint64
}

// FoundingRelationship returns the founding operator relationship, or nil if none.
func (s *Store) FoundingRelationship() (*Relationship, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var r Relationship
	var updatedSeq sql.NullInt64
	err := s.db.QueryRow(`
		SELECT id, counterpart_name, counterpart_role, trust_level, autonomy_level,
		       relationship_type, charter_text, created_seq, updated_seq
		FROM relationships
		WHERE relationship_type = 'founding_operator'
		ORDER BY created_seq ASC
		LIMIT 1
	`).Scan(&r.ID, &r.CounterpartName, &r.CounterpartRole, &r.TrustLevel,
		&r.AutonomyLevel, &r.RelationshipType, &r.CharterText, &r.CreatedSeq, &updatedSeq)
	if err != nil {
		return nil, err
	}
	if updatedSeq.Valid {
		r.UpdatedSeq = uint64(updatedSeq.Int64)
	}
	return &r, nil
}

func (s *Store) CurrentOperatorRelationship() (*Relationship, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var r Relationship
	var updatedSeq sql.NullInt64
	err := s.db.QueryRow(`
		SELECT id, counterpart_name, counterpart_role, trust_level, autonomy_level,
		       relationship_type, charter_text, created_seq, updated_seq
		FROM relationships
		WHERE counterpart_role = 'operator' AND superseded_by IS NULL
		ORDER BY created_seq DESC LIMIT 1
	`).Scan(&r.ID, &r.CounterpartName, &r.CounterpartRole, &r.TrustLevel,
		&r.AutonomyLevel, &r.RelationshipType, &r.CharterText, &r.CreatedSeq, &updatedSeq)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if updatedSeq.Valid {
		r.UpdatedSeq = uint64(updatedSeq.Int64)
	}
	return &r, nil
}

// CharterNarrative returns the current operator charter text — Ring 1's
// authored document, rendered verbatim in the prompt. Latest non-superseded
// operator relationship wins (succession: a successor operator's charter
// replaces the founding one).
func (s *Store) CharterNarrative() (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	text, _, err := s.operatorCharterLocked()
	return text, err
}

func (s *Store) operatorCharterLocked() (string, bool, error) {
	var text string
	err := s.db.QueryRow(`
		SELECT charter_text FROM relationships
		WHERE counterpart_role = 'operator' AND superseded_by IS NULL
		ORDER BY created_seq DESC LIMIT 1
	`).Scan(&text)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	return text, err == nil, err
}

// IdentityName returns the identity's name from the genesis event in the
// ledger mirror — the name is born at genesis and survives config loss.
// Empty string if not found (never an error: the caller renders what exists).
func (s *Store) IdentityName() string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var payload []byte
	err := s.db.QueryRow(
		`SELECT payload FROM ledger WHERE type = 'ring0.genesis' ORDER BY seq ASC LIMIT 1`,
	).Scan(&payload)
	if err != nil {
		return ""
	}
	var p struct {
		Name string `json:"name"`
	}
	json.Unmarshal(payload, &p)
	return p.Name
}

// (MaterializeRelationship deleted, M8 2026-08-17 external review: an
// exported materializer that skipped every R52 check, zero non-test
// callers — a wiring mistake away from reopening the consent hole. The
// one true path is Store.Materialize.)

// Commitment is a promise with a counterpart (Q1).
