package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/tokenestimate"
)

const CharterDefaultMaxTokens = 2000

func (s *Store) SetCharterMaxTokens(n int) {
	if n <= 0 {
		n = CharterDefaultMaxTokens
	}
	s.charterMaxTokens.Store(int64(n))
}

func (s *Store) CharterMaxTokens() int {
	if n := s.charterMaxTokens.Load(); n > 0 {
		return int(n)
	}
	return CharterDefaultMaxTokens
}

type CharterBudgetRefusal struct {
	Tokens, Max int
}

func (e *CharterBudgetRefusal) Error() string {
	return fmt.Sprintf("relationship.upsert refused: bounded charter: charter_text is approximately %d tokens; maximum is %d (prompt.charter_max_tokens). Your charter rides whole in every prompt. Shorten it, say the shorter charter to your operator, and record it once they have affirmed it; or ask them to raise the budget (Settings → Technical → Reflection). The charter you hold now, sent again unchanged, is admitted", e.Tokens, e.Max)
}

func (s *Store) admitCharterBudgetLocked(payload []byte) error {
	p, err := readRelationshipPayload(payload)
	if err != nil || p.CounterpartRole != "operator" || p.CharterText == "" || s.validateRelationshipLocked(invariantsOf(p)) != nil {
		return nil
	}
	budget := s.CharterMaxTokens()
	tokens := tokenestimate.EstimateWire(p.CharterText)
	if tokens <= budget {
		return nil
	}
	var recorded string
	err = s.h().QueryRow(`
		SELECT charter_text FROM relationships
		WHERE counterpart_role = 'operator' AND superseded_by IS NULL
		ORDER BY created_seq DESC LIMIT 1`).Scan(&recorded)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("read the recorded charter: %w", err)
	}
	if err == nil && recorded == p.CharterText {
		return nil
	}
	return &CharterBudgetRefusal{Tokens: tokens, Max: budget}
}

func (s *Store) CurrentOperatorRelationship() (*Relationship, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var r Relationship
	var updatedSeq sql.NullInt64
	err := s.db.QueryRow(`
		SELECT id, counterpart_name, counterpart_role, trust_level, autonomy_level,
		       relationship_type, charter_text, approval_digest, approved_level, created_seq, updated_seq
		FROM relationships
		WHERE counterpart_role = 'operator' AND superseded_by IS NULL
		ORDER BY created_seq DESC LIMIT 1
	`).Scan(&r.ID, &r.CounterpartName, &r.CounterpartRole, &r.TrustLevel,
		&r.AutonomyLevel, &r.RelationshipType, &r.CharterText, &r.ApprovalDigest, &r.ApprovedLevel, &r.CreatedSeq, &updatedSeq)
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

func (s *Store) OtherCurrentRelationships() ([]Relationship, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rows, err := s.db.Query(`
		SELECT id, counterpart_name, counterpart_role, trust_level, autonomy_level,
		       relationship_type, charter_text, created_seq, updated_seq
		FROM relationships
		WHERE superseded_by IS NULL
		  AND id IS NOT (SELECT id FROM relationships
		                  WHERE counterpart_role = 'operator' AND superseded_by IS NULL
		                  ORDER BY created_seq DESC LIMIT 1)
		ORDER BY created_seq`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Relationship
	for rows.Next() {
		var r Relationship
		var updatedSeq sql.NullInt64
		if err := rows.Scan(&r.ID, &r.CounterpartName, &r.CounterpartRole, &r.TrustLevel,
			&r.AutonomyLevel, &r.RelationshipType, &r.CharterText, &r.CreatedSeq, &updatedSeq); err != nil {
			return nil, err
		}
		if updatedSeq.Valid {
			r.UpdatedSeq = uint64(updatedSeq.Int64)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) RelationshipAsHeld(id string) (rel *Relationship, ring1 bool, err error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var r Relationship
	var updatedSeq sql.NullInt64
	err = s.db.QueryRow(`
		SELECT id, counterpart_name, counterpart_role, trust_level, autonomy_level,
		       relationship_type, charter_text, created_seq, updated_seq, COALESCE(superseded_by, ''),
		       id IS (SELECT id FROM relationships
		               WHERE counterpart_role = 'operator' AND superseded_by IS NULL
		               ORDER BY created_seq DESC LIMIT 1)
		FROM relationships WHERE id = ?
	`, id).Scan(&r.ID, &r.CounterpartName, &r.CounterpartRole, &r.TrustLevel,
		&r.AutonomyLevel, &r.RelationshipType, &r.CharterText, &r.CreatedSeq, &updatedSeq, &r.SupersededBy, &ring1)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if updatedSeq.Valid {
		r.UpdatedSeq = uint64(updatedSeq.Int64)
	}
	return &r, ring1, nil
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
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", false, nil
	}
	return charter, name, err == nil, err
}

func (s *Store) AboutOperator() (AboutOperator, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.aboutOperatorLocked()
}

func (s *Store) aboutOperatorLocked() (AboutOperator, error) {
	var about AboutOperator
	err := s.db.QueryRow(`
		SELECT id, COALESCE(counterpart_name, '') FROM relationships
		WHERE counterpart_role = 'operator' AND superseded_by IS NULL
		ORDER BY created_seq DESC LIMIT 1
	`).Scan(&about.RelationshipID, &about.Name)
	if errors.Is(err, sql.ErrNoRows) {
		return AboutOperator{}, nil
	}
	if err != nil {
		return AboutOperator{}, err
	}
	rows, err := s.db.Query(`
		SELECT b.id, b.statement FROM beliefs b
		WHERE b.ring = 3 AND b.archived = 0 AND b.superseded_by IS NULL
		  AND EXISTS (SELECT 1 FROM edges e WHERE e.from_id = ? AND e.to_id = b.id
		              AND e.edge_type = 'SHAPED_BY' AND e.archived = 0)
		ORDER BY b.last_seq DESC, b.id`, about.RelationshipID)
	if err != nil {
		return AboutOperator{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var b OperatorBelief
		if err := rows.Scan(&b.ID, &b.Statement); err != nil {
			return AboutOperator{}, err
		}
		about.Beliefs = append(about.Beliefs, b)
	}
	return about, rows.Err()
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
