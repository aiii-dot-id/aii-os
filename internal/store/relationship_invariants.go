package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

type relationshipInvariants struct {
	ID                string
	CounterpartRole   string
	RelationshipType  string
	Supersedes        string
	OperatorApproval  string
	ApprovalTurn      uint64
	ApprovalBasis     string
	AutonomyLevel     string
	ApprovalDigest    string
	ApprovalPressTurn uint64
}

func (p relationshipInvariants) approvalShape() error {
	if p.CounterpartRole != "operator" {
		return nil
	}
	if p.OperatorApproval == "" {
		return fmt.Errorf("operator relationship requires operator_approval_excerpt — Ring 1 is not identity-unilateral")
	}
	if p.ApprovalBasis != "conversation_turn" {
		return fmt.Errorf("operator relationship requires approval_basis %q (got %q)", "conversation_turn", p.ApprovalBasis)
	}

	if p.ApprovalTurn <= 0 {
		return fmt.Errorf("conversation_turn basis requires operator_approval_turn")
	}

	if p.ApprovalDigest != "" && p.ApprovalDigest != AutonomyApprovalDigest(p.ID, p.AutonomyLevel) {
		return fmt.Errorf("approval_digest does not bind autonomy_level %q of %q", p.AutonomyLevel, p.ID)
	}
	if p.ApprovalDigest != "" && p.ApprovalPressTurn == 0 {
		return fmt.Errorf("approval_digest cites no press (approval_press_turn)")
	}
	return nil
}

func (s *Store) validateRelationshipLocked(p relationshipInvariants) error {
	if err := p.approvalShape(); err != nil {
		return err
	}

	if p.CounterpartRole == "operator" {
		var currentID string
		err := s.h().QueryRow(
			`SELECT id FROM relationships
			  WHERE counterpart_role = 'operator' AND superseded_by IS NULL AND id != ?
			  ORDER BY created_seq DESC LIMIT 1`, p.ID,
		).Scan(&currentID)
		switch {
		case errors.Is(err, sql.ErrNoRows):

		case err != nil:
			return fmt.Errorf("current operator relationship check: %w", err)
		case p.Supersedes == "":
			return fmt.Errorf("operator relationship %q must supersede the current one (%q) — "+
				"two unsuperseded operator relationships fork Ring 1, and the identity would answer to whichever sorted last",
				p.ID, currentID)
		case p.Supersedes != currentID:
			return fmt.Errorf("operator relationship %q supersedes %q, but the CURRENT operator relationship is %q — "+
				"superseding an already-superseded row leaves Ring 1 forked",
				p.ID, p.Supersedes, currentID)
		}
	}

	if p.Supersedes != "" {
		var supersededRole string
		err := s.h().QueryRow(
			`SELECT counterpart_role FROM relationships WHERE id = ?`, p.Supersedes,
		).Scan(&supersededRole)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("supersedes %q — no such relationship: succession must name a real row", p.Supersedes)
		}
		if err != nil {
			return fmt.Errorf("supersede role check: %w", err)
		}
		if supersededRole == "operator" && p.CounterpartRole != "operator" {
			return fmt.Errorf("relationship %q (role %q) cannot supersede operator relationship %q — operator succession requires an operator-role successor with its own evidence", p.ID, p.CounterpartRole, p.Supersedes)
		}
	}
	return nil
}

type relationshipPayload = struct {
	ID                string `json:"id"`
	CounterpartName   string `json:"counterpart_name"`
	CounterpartRole   string `json:"counterpart_role"`
	TrustLevel        string `json:"trust_level"`
	AutonomyLevel     string `json:"autonomy_level"`
	RelationshipType  string `json:"relationship_type"`
	Supersedes        string `json:"supersedes"`
	CharterText       string `json:"charter_text"`
	OperatorApproval  string `json:"operator_approval_excerpt"`
	ApprovalTurn      uint64 `json:"operator_approval_turn"`
	ApprovalBasis     string `json:"approval_basis"`
	ApprovalDigest    string `json:"approval_digest"`
	ApprovalPressTurn uint64 `json:"approval_press_turn"`
}

func invariantsOf(p relationshipPayload) relationshipInvariants {
	return relationshipInvariants{
		ID: p.ID, CounterpartRole: p.CounterpartRole, RelationshipType: p.RelationshipType,
		Supersedes: p.Supersedes, OperatorApproval: p.OperatorApproval,
		ApprovalTurn: p.ApprovalTurn, ApprovalBasis: p.ApprovalBasis,
		AutonomyLevel: p.AutonomyLevel, ApprovalDigest: p.ApprovalDigest, ApprovalPressTurn: p.ApprovalPressTurn,
	}
}

func readRelationshipPayload(raw []byte) (relationshipPayload, error) {
	var p relationshipPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return p, fmt.Errorf("parse relationship payload: %w", err)
	}

	if p.CounterpartRole == "" {
		p.CounterpartRole = "operator"
	}

	if p.TrustLevel == "" {
		p.TrustLevel = "building"
	}
	if p.AutonomyLevel == "" {
		p.AutonomyLevel = "supervised"
	}
	if p.RelationshipType == "" {
		p.RelationshipType = "founding_operator"
	}
	return p, nil
}
