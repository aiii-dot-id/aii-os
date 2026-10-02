package store

import (
	"context"
	"database/sql"

	"github.com/aiii-dot-id/aii-os/internal/interaction"
	"github.com/google/uuid"
)

func (s *Store) recordWorkChangeTx(ctx context.Context, tx *sql.Tx, change interaction.WorkChange, turn, reason string) error {
	var project string
	var child bool
	if err := tx.QueryRow(`SELECT project_id, `+queuedChild("")+` FROM work_sessions WHERE id=?`,
		SubagentWorkKind, SubagentWorkKind, change.Session).Scan(&project, &child); err != nil {
		return err
	}
	_, _, err := s.appendInteractionTx(ctx, tx, InteractionInput{ID: "work_" + uuid.NewString(), Kind: interaction.Notice, Role: interaction.System, ProjectID: project, TurnID: turn, Content: reason, Details: interaction.Details{Actor: workActor(child), WorkSession: change.Session, Work: &change}})
	return err
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
