package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/aiii-dot-id/aii-os/internal/interaction"
)

type ToolMeasurement struct {
	DurationMS                             *int64
	WorkSession, SessionReason, ReasonCode string
}

func (s *Store) appendToolOutcome(tx *sql.Tx, turn string, ordinal int, tool, args, result string, outcome interaction.Outcome, truncated bool, m ToolMeasurement) (bool, error) {
	id := toolExecutionID(turn, ordinal)
	var parent, project, session, parentDetails string
	if err := tx.QueryRow(`SELECT id,project_id,session_id,details FROM conversations WHERE source_kind='tool' AND source_id=? AND kind='tool_call'`, id).Scan(&parent, &project, &session, &parentDetails); err != nil {
		return false, err
	}
	var prior interaction.Details
	if err := json.Unmarshal([]byte(parentDetails), &prior); err != nil {
		return false, err
	}

	if m.WorkSession == "" && m.SessionReason == "" {
		m.WorkSession = prior.WorkSession
		m.SessionReason = prior.SessionReason
	}
	_, added, err := s.appendInteractionTx(context.Background(), tx, InteractionInput{ID: "tool_result_" + id, Kind: interaction.ToolResult, Role: interaction.System, TurnID: turn, SessionID: session, ProjectID: project, RelatedID: parent, Source: &interaction.Source{Kind: "tool", ID: id}, Content: toolExcerpt(tool, args, result), Outcome: outcome, Details: interaction.Details{Tool: tool, Actor: prior.Actor, Model: prior.Model, ProviderCallID: prior.ProviderCallID, Ordinal: ordinal, Truncated: truncated, DurationMS: m.DurationMS, WorkSession: m.WorkSession, SessionReason: m.SessionReason, ReasonCode: m.ReasonCode, ArgsRecord: metaRecord(args), ResultRecord: metaRecord(result)}})
	return added, err
}

func (s *Store) RecordToolDoneMeasured(turn string, ordinal int, tool, args, result string, outcome interaction.Outcome, truncated bool, m ToolMeasurement) error {
	changed := false
	defer s.notifyInteraction(&changed)
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.w().Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var n int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM conversations c WHERE c.source_kind='tool' AND c.source_id=? AND `+openToolCall, toolExecutionID(turn, ordinal)).Scan(&n); err != nil {
		return err
	}
	if n != 1 {
		return fmt.Errorf("tool completion for turn %s ordinal %d matched %d open call(s), want exactly 1", turn, ordinal, n)
	}
	added, err := s.appendToolOutcome(tx, turn, ordinal, tool, args, result, outcome, truncated, m)
	if err != nil {
		return err
	}
	err = tx.Commit()
	changed = added
	return err
}
