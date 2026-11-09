package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/aiii-dot-id/aii-os/internal/interaction"
	"github.com/google/uuid"
)

const (
	StageSupervised = "supervised"
	StageTrusted    = "trusted"
	StageAutonomous = "autonomous"
)

func AutonomyApprovalDigest(counterpart, level string) string {
	raw, _ := json.Marshal(map[string]string{"autonomy_level": level, "counterpart": counterpart})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func ApprovalPressContent(name, level, digest string) string {
	return fmt.Sprintf("Let %s run as %s. (approval %s)", name, level, digest)
}

func IsApprovalPress(content, digest string) bool {
	return digest != "" && strings.HasSuffix(strings.TrimSpace(content), "(approval "+digest+")")
}

const AnnotationPress = "press"

func (s *Store) pressTurnLocked(turn uint64, digest string) error {
	if turn == 0 {
		return fmt.Errorf("approval_digest cites no press")
	}
	var role, content string
	if err := s.h().QueryRow(`SELECT role, content FROM conversations WHERE turn_seq = ?`, turn).Scan(&role, &content); err != nil {
		return fmt.Errorf("approval_press_turn %d: %w", turn, err)
	}
	said, err := saidOf(s.h(), turn)
	if err != nil {
		return err
	}
	var pressed bool
	if err := s.h().QueryRow(`SELECT EXISTS(SELECT 1 FROM turn_annotations WHERE turn_seq = ? AND kind = ? AND key = ?)`, turn, AnnotationPress, digest).Scan(&pressed); err != nil {
		return err
	}
	if role != "operator" || said != interaction.SaidAnswer || !pressed || !IsApprovalPress(content, digest) {
		return fmt.Errorf("approval_press_turn %d is not the operator's press on the page for this digest", turn)
	}
	return nil
}

func (s *Store) LatestOperatorPress(digest string) (*ConversationTurn, error) {
	if digest == "" {
		return nil, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	var t ConversationTurn
	err := scanTurn(s.db.QueryRow(`SELECT id, session_id, role, content, turn_seq, created_at FROM conversations
		WHERE role = 'operator' AND kind = ? AND EXISTS (SELECT 1 FROM turn_annotations m WHERE m.turn_seq = conversations.turn_seq AND m.kind = ? AND m.key = ?)
		AND EXISTS (SELECT 1 FROM turn_annotations p WHERE p.turn_seq = conversations.turn_seq AND p.kind = ? AND p.key = ?)
		ORDER BY turn_seq DESC LIMIT 1`, string(interaction.Message), AnnotationSaid, interaction.SaidAnswer, AnnotationPress, digest), &t)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !IsApprovalPress(t.Content, digest) {
		return nil, nil
	}
	return &t, nil
}

func (s *Store) RecordOperatorPress(ctx context.Context, name, level, digest string) (InteractionRef, error) {
	changed := false
	defer s.notifyInteraction(&changed)
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.recordInteractionLocked(ctx, InteractionInput{ID: "press_" + uuid.New().String(), Kind: interaction.Message, Role: interaction.Operator,
		Content: ApprovalPressContent(name, level, digest), SessionID: "default", Details: interaction.Details{Said: interaction.SaidAnswer}},
		&changed, &RowAnnotation{Kind: AnnotationPress, Key: digest, Payload: "{}"})
}

func (s *Store) ApprovedStage() (string, error) {
	rel, err := s.CurrentOperatorRelationship()
	if err != nil {
		return "", err
	}

	if rel == nil || rel.ApprovalDigest == "" || rel.ApprovedLevel == "" || rel.ApprovalDigest != AutonomyApprovalDigest(rel.ID, rel.ApprovedLevel) {
		return StageAutonomous, nil
	}
	return rel.ApprovedLevel, nil
}
