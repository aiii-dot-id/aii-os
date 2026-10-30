package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/aiii-dot-id/aii-os/internal/interaction"
)

const AnnotationCheck = "check"

const (
	CheckTool  = "check"
	CheckActor = "host"
)

type checkDeclaration struct {
	Command string `json:"command"`

	Calls int `json:"calls"`
}

type WorkCheck struct {
	Command string

	SetAfterCalls int

	Ran        bool
	Outcome    string
	ExitStatus *int
	DurationMS *int64
	ReasonCode string
}

func checkExecution(session string) string { return "check_" + session }

func (s *Store) declareCheckTx(ctx context.Context, tx *sql.Tx, session, command string) error {
	var calls int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM conversations WHERE kind='tool_call' AND json_extract(details,'$.work_session_id')=?`, session).Scan(&calls); err != nil {
		return err
	}
	reason := "Work check declared"
	if command == "" {
		reason = "Work check withdrawn"
	}
	ref, err := s.recordWorkChangeRefTx(ctx, tx, interaction.WorkChange{Session: session}, interaction.TurnID(ctx), reason)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(checkDeclaration{Command: command, Calls: calls})
	if err != nil {
		return err
	}
	return s.annotateTurnTx(ctx, tx, ref.Sequence, AnnotationCheck, session, string(payload))
}

func (s *Store) SetWorkCheck(ctx context.Context, session, command string) error {
	changed := false
	defer s.notifyInteraction(&changed)
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.w().BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var status string
	if err := tx.QueryRowContext(ctx, `SELECT status FROM work_sessions WHERE id=?`, session).Scan(&status); err != nil {
		return fmt.Errorf("work session %s: %w", session, err)
	}
	if status != "active" {
		return fmt.Errorf("work session %s is %s: a check is declared before delivery", session, status)
	}
	if err := s.declareCheckTx(ctx, tx, session, command); err != nil {
		return err
	}
	err = tx.Commit()
	changed = err == nil
	return err
}

func (s *Store) WorkCheckOf(session string) (WorkCheck, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var c WorkCheck
	var payload string
	err := s.db.QueryRow(`SELECT payload FROM turn_annotations WHERE kind=? AND key=? ORDER BY turn_seq DESC LIMIT 1`, AnnotationCheck, session).Scan(&payload)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return c, err
	}
	if err == nil {
		var d checkDeclaration
		if err := json.Unmarshal([]byte(payload), &d); err != nil {
			return c, fmt.Errorf("the check of %s does not decode: %w", session, err)
		}
		c.Command, c.SetAfterCalls = d.Command, d.Calls
	}
	var details string
	err = s.db.QueryRow(`SELECT outcome, details FROM conversations WHERE source_kind='tool' AND source_id=? AND kind='tool_result'`, checkExecution(session)).Scan(&c.Outcome, &details)
	if errors.Is(err, sql.ErrNoRows) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	var d interaction.Details
	if err := json.Unmarshal([]byte(details), &d); err != nil {
		return c, fmt.Errorf("the check run of %s does not decode: %w", session, err)
	}
	c.Ran, c.ExitStatus, c.DurationMS, c.ReasonCode = true, d.ExitStatus, d.DurationMS, d.ReasonCode
	return c, nil
}

func (s *Store) WorkChecksOf(sessions []string) (map[string]WorkCheck, error) {
	out := map[string]WorkCheck{}
	if len(sessions) == 0 {
		return out, nil
	}
	ids, err := json.Marshal(sessions)
	if err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	rows, err := s.db.Query(`SELECT key, payload FROM turn_annotations WHERE kind=? AND key IN (SELECT value FROM json_each(?)) ORDER BY turn_seq`, AnnotationCheck, string(ids))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var key, payload string
		if err := rows.Scan(&key, &payload); err != nil {
			return nil, err
		}
		var d checkDeclaration
		if err := json.Unmarshal([]byte(payload), &d); err != nil {
			return nil, fmt.Errorf("the check of %s does not decode: %w", key, err)
		}
		out[key] = WorkCheck{Command: d.Command, SetAfterCalls: d.Calls}
	}
	return out, rows.Err()
}

func (s *Store) RecordWorkCheckRun(ctx context.Context, session, command, result string, outcome interaction.Outcome, truncated bool, m ToolMeasurement) error {
	args, err := json.Marshal(map[string]string{"command": command})
	if err != nil {
		return err
	}
	changed := false
	defer s.notifyInteraction(&changed)
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.w().BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var project string
	if err := tx.QueryRowContext(ctx, `SELECT project_id FROM work_sessions WHERE id=?`, session).Scan(&project); err != nil {
		return fmt.Errorf("work session %s: %w", session, err)
	}
	id, turn := checkExecution(session), interaction.TurnID(ctx)
	call, added, err := s.appendInteractionTx(ctx, tx, InteractionInput{ID: "tool_call_" + id, Kind: interaction.ToolCall, Role: interaction.System, TurnID: turn, ProjectID: project,
		Source: &interaction.Source{Kind: "tool", ID: id}, Content: toolExcerpt(CheckTool, string(args), "", 0),
		Details: interaction.Details{WorkSession: session, Tool: CheckTool, Actor: CheckActor, ArgsRecord: metaRecord(string(args))}})
	if err != nil {
		return err
	}
	if !added {
		return fmt.Errorf("the check of %s already ran", session)
	}
	if _, _, err := s.appendInteractionTx(ctx, tx, InteractionInput{ID: "tool_result_" + id, Kind: interaction.ToolResult, Role: interaction.System, TurnID: turn, ProjectID: project,
		RelatedID: call.ID, Source: &interaction.Source{Kind: "tool", ID: id}, Content: toolExcerpt(CheckTool, string(args), result, s.ToolResultRunes()), Outcome: outcome,
		Details: interaction.Details{WorkSession: session, Tool: CheckTool, Actor: CheckActor, Truncated: truncated, DurationMS: m.DurationMS, ExitStatus: m.ExitStatus, ReasonCode: m.ReasonCode,
			ArgsRecord: metaRecord(string(args)), ResultRecord: metaRecord(result)}}); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	changed = true
	return nil
}
