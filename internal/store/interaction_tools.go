package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/interaction"
)

type ToolMeasurement struct {
	DurationMS                             *int64
	WorkSession, SessionReason, ReasonCode string
}

func (s *Store) appendToolOutcome(tx *sql.Tx, turn string, ordinal int, tool, args, result string, outcome interaction.Outcome, truncated bool, m ToolMeasurement) (bool, error) {
	var id, actor, model, call string
	if err := tx.QueryRow(`SELECT execution_id,actor,model,provider_call_id FROM tool_events WHERE turn_id=? AND ordinal=?`, turn, ordinal).Scan(&id, &actor, &model, &call); err != nil {
		return false, err
	}
	var parent string
	err := tx.QueryRow(`SELECT id FROM conversations WHERE kind='tool_call' AND source_kind='tool' AND source_id=?`, id).Scan(&parent)
	if err == sql.ErrNoRows {

		_, added, e := s.appendInteractionTx(context.Background(), tx, InteractionInput{ID: "tool_legacy_" + id, Kind: interaction.Notice, Role: interaction.System, TurnID: turn, ProjectID: s.activeProject, Content: toolExcerpt(tool, args, result), Outcome: outcome, Details: interaction.Details{Reason: "legacy tool execution has no interaction call reference", Tool: tool, Actor: actor, Model: model, Truncated: truncated}})
		return added, e
	}
	if err != nil {
		return false, err
	}

	var project, session, parentDetails string
	if err = tx.QueryRow(`SELECT project_id,session_id,details FROM conversations WHERE id=?`, parent).Scan(&project, &session, &parentDetails); err != nil {
		return false, err
	}
	if m.WorkSession == "" && m.SessionReason == "" {
		var prior interaction.Details
		if err := json.Unmarshal([]byte(parentDetails), &prior); err != nil {
			return false, err
		}
		m.WorkSession = prior.WorkSession
		m.SessionReason = prior.SessionReason
	}
	_, added, err := s.appendInteractionTx(context.Background(), tx, InteractionInput{ID: "tool_result_" + id, Kind: interaction.ToolResult, Role: interaction.System, TurnID: turn, SessionID: session, ProjectID: project, RelatedID: parent, Source: &interaction.Source{Kind: "tool", ID: id}, Content: toolExcerpt(tool, args, result), Outcome: outcome, Details: interaction.Details{Tool: tool, Actor: actor, Model: model, ProviderCallID: call, Ordinal: ordinal, Truncated: truncated, DurationMS: m.DurationMS, WorkSession: m.WorkSession, SessionReason: m.SessionReason, ReasonCode: m.ReasonCode, ArgsRecord: metaRecord(args), ResultRecord: metaRecord(result)}})
	return added, err
}
func (s *Store) RecordToolDoneMeasured(turn string, ordinal int, tool, args, result string, failed, truncated bool, m ToolMeasurement) error {
	changed := false
	defer s.notifyInteraction(&changed)
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.w().Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	f, tr := 0, 0
	if failed {
		f = 1
	}
	if truncated {
		tr = 1
	}
	res, err := tx.Exec(`UPDATE tool_events SET state='done',failed=?,truncated=?,result_record=?,finished_ms=? WHERE turn_id=? AND ordinal=? AND state='started'`, f, tr, metaRecord(result), time.Now().UTC().UnixMilli(), turn, ordinal)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return fmt.Errorf("tool completion for turn %s ordinal %d matched %d started row(s), want exactly 1", turn, ordinal, n)
	}
	outcome := interaction.Succeeded
	if failed {
		outcome = interaction.Failed
	}
	added, err := s.appendToolOutcome(tx, turn, ordinal, tool, args, result, outcome, truncated, m)
	if err != nil {
		return err
	}
	err = tx.Commit()
	changed = added
	return err
}
