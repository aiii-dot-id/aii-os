package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
)

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

type CharterApproval struct {
	Seq     uint64
	Excerpt string
	Turn    uint64
	State   ApprovalState
	SaidAt  string
}

type ApprovalState string

const (
	ApprovalSaid    ApprovalState = "said"
	ApprovalGone    ApprovalState = "gone"
	ApprovalDiffers ApprovalState = "differs"
	ApprovalUncited ApprovalState = "uncited"
)

func (s *Store) CharterApproval(rel *Relationship) (CharterApproval, error) {
	if rel == nil || rel.CharterText == "" {
		return CharterApproval{}, fmt.Errorf("charter approval: there is no charter to cite an approval for")
	}
	last := rel.UpdatedSeq
	if last == 0 {
		last = rel.CreatedSeq
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	var seq uint64
	var payload string
	err := s.db.QueryRow(`
		SELECT seq, payload FROM ledger
		 WHERE seq BETWEEN ? AND ? AND type = ?
		   AND json_extract(payload, '$.id') = ?
		   AND json_extract(payload, '$.charter_text') != ''
		 ORDER BY seq DESC LIMIT 1`,
		rel.CreatedSeq, last, string(ledger.EventRelationshipUpsert), rel.ID,
	).Scan(&seq, &payload)
	if errors.Is(err, sql.ErrNoRows) {
		return CharterApproval{}, fmt.Errorf("charter approval: no signed relationship.upsert of %s in records %d to %d wrote a charter — the projection and the record disagree", rel.ID, rel.CreatedSeq, last)
	}
	if err != nil {
		return CharterApproval{}, fmt.Errorf("charter approval: %w", err)
	}

	var p struct {
		ID          string `json:"id"`
		CharterText string `json:"charter_text"`
		Excerpt     string `json:"operator_approval_excerpt"`
		Turn        uint64 `json:"operator_approval_turn"`
	}
	if err := json.Unmarshal([]byte(payload), &p); err != nil {
		return CharterApproval{}, fmt.Errorf("charter approval: record %d: %w", seq, err)
	}
	if p.ID != rel.ID || p.CharterText != rel.CharterText {
		return CharterApproval{}, fmt.Errorf("charter approval: record %d did not write the charter %s holds — the projection and the record disagree", seq, rel.ID)
	}
	a := CharterApproval{Seq: seq, Excerpt: p.Excerpt, Turn: p.Turn, State: ApprovalUncited}
	if p.Excerpt == "" || p.Turn == 0 {
		return a, nil
	}
	var role, content, at string
	err = s.db.QueryRow(`SELECT role, content, created_at FROM conversations WHERE turn_seq = ?`, p.Turn).Scan(&role, &content, &at)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		a.State = ApprovalGone
	case err != nil:
		return CharterApproval{}, fmt.Errorf("charter approval: turn %d: %w", p.Turn, err)
	case role != "operator" || content != p.Excerpt:
		a.State = ApprovalDiffers
	default:
		a.State, a.SaidAt = ApprovalSaid, at
	}
	return a, nil
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
