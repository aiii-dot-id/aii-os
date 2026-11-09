package store

import (
	"context"
	"database/sql"
	"uuid"

	"github.com/aiii-dot-id/aii-os/internal/interaction"
)

func (s *Store) recordWorkChangeTx(ctx context.Context, tx *sql.Tx, change interaction.WorkChange, turn, reason string) error {
	_, err := s.recordWorkChangeRefTx(ctx, tx, change, turn, reason)
	return err
}

func (s *Store) recordWorkChangeRefTx(ctx context.Context, tx *sql.Tx, change interaction.WorkChange, turn, reason string) (InteractionRef, error) {
	var project string
	var child bool
	if err := tx.QueryRow(`SELECT project_id, `+queuedChild("")+` FROM work_sessions WHERE id=?`,
		SubagentWorkKind, SubagentWorkKind, change.Session).Scan(&project, &child); err != nil {
		return InteractionRef{}, err
	}
	ref, _, err := s.appendInteractionTx(ctx, tx, InteractionInput{ID: "work_" + uuid.New().String(), Kind: interaction.Notice, Role: interaction.System, ProjectID: project, TurnID: turn, Content: reason, Details: interaction.Details{Actor: workActor(child), WorkSession: change.Session, Work: &change}})
	return ref, err
}

func (s *Store) WorkDeliveredInTurn(turn string) (bool, error) {
	if turn == "" {
		return false, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	var delivered bool
	err := s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM conversations WHERE turn_id=? AND kind=? AND json_extract(details,'$.work.result') IS NOT NULL)`, turn, string(interaction.Notice)).Scan(&delivered)
	return delivered, err
}

func (s *Store) RecordWorkForecast(session, turn string, steps, independent *int) error {
	changed := false
	defer s.notifyInteraction(&changed)
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.w().Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = s.recordWorkChangeTx(context.Background(), tx, interaction.WorkChange{Session: session, Steps: steps, Independent: independent}, turn, "Work forecast accepted"); err != nil {
		return err
	}
	err = tx.Commit()
	changed = true
	return err
}
