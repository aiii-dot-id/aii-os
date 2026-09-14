package store

import (
	"database/sql"
	"encoding/json"
	"errors"
)

// .

// .
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

// .
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
	if errors.Is(err, sql.ErrNoRows) {
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

// .
// .
// .
// .
func (s *Store) CharterNarrative() (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	text, _, _, err := s.operatorCharterLocked()
	return text, err
}

func (s *Store) operatorCharterLocked() (charter, name string, has bool, err error) {
	err = s.db.QueryRow(`
		SELECT charter_text, COALESCE(counterpart_name, '') FROM relationships
		WHERE counterpart_role = 'operator' AND superseded_by IS NULL
		ORDER BY created_seq DESC LIMIT 1
	`).Scan(&charter, &name)
	if err == sql.ErrNoRows {
		return "", "", false, nil
	}
	return charter, name, err == nil, err
}

// .
// .
// .
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

// .
// .
// .
// .

// .
