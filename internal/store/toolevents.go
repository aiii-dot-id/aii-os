package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/interaction"
	"strings"
	"time"
)

const (
	toolEventRetention = 14 * 24 * time.Hour
)

func metaRecord(s string) string {
	return fmt.Sprintf("[%d chars, sha256=%x]", len([]rune(s)), sha256.Sum256([]byte(s)))
}

type LegacyStart struct {
	TsMs   int64
	CallID string
	Tool   string
	Args   string
}

func (s *Store) RecordToolStart(turnID string, ordinal int, actor, model, providerCallID, tool, args string) error {
	changed := false
	defer s.notifyInteraction(&changed)
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.w().Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	id := fmt.Sprintf("tex_%x", sha256.Sum256([]byte(fmt.Sprintf("%s/%d", turnID, ordinal))))
	_, err = tx.Exec(`INSERT INTO tool_events(execution_id,turn_id,ordinal,actor,model,provider_call_id,tool,args_record,state,started_ms) VALUES(?,?,?,?,?,?,?,?,'started',?)`, id, turnID, ordinal, actor, model, providerCallID, tool, metaRecord(args), time.Now().UTC().UnixMilli())
	if err != nil {
		return err
	}
	project, work, why := s.activeProject, "", ""
	switch actor {
	case "operator":
		why = "operator_act"
	case "main", "safe":
		err = tx.QueryRow(`SELECT id FROM work_sessions WHERE status='active' AND NOT EXISTS(SELECT 1 FROM work_queue w WHERE `+subagentQueueForSession+`) ORDER BY rowid DESC LIMIT 1`, SubagentWorkKind).Scan(&work)
		if err == sql.ErrNoRows {
			why = "no_session"
		} else if err != nil {
			return err
		}
	default:
		project = ""
		err = tx.QueryRow(`SELECT id,project_id FROM work_sessions WHERE id=?`, actor).Scan(&work, &project)
		if err == sql.ErrNoRows {
			why = "acting_session_unavailable"
		} else if err != nil {
			return err
		}
	}
	_, added, err := s.appendInteractionTx(context.Background(), tx, InteractionInput{ID: "tool_call_" + id, Kind: interaction.ToolCall, Role: interaction.System, TurnID: turnID, ProjectID: project, Source: &interaction.Source{Kind: "tool", ID: id}, Content: toolExcerpt(tool, args, ""), Details: interaction.Details{WorkSession: work, SessionReason: why, Tool: tool, Actor: actor, Model: model, ProviderCallID: providerCallID, Ordinal: ordinal, ArgsRecord: metaRecord(args)}})
	if err != nil {
		return err
	}
	err = tx.Commit()
	changed = added
	return err

}

func (s *Store) RecordToolDone(turnID string, ordinal int, tool, args, result string, failed, truncated bool) error {
	return s.RecordToolDoneMeasured(turnID, ordinal, tool, args, result, failed, truncated, ToolMeasurement{})
}

func (s *Store) ToolCallStarted(turnID string, ordinal int) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM tool_events WHERE turn_id=? AND ordinal=? AND state='started'`,
		turnID, ordinal).Scan(&n); err != nil {
		return false, err
	}
	return n > 0, nil
}

func (s *Store) AbandonUnfinishedToolCalls() ([]string, error) {
	changed := false
	defer s.notifyInteraction(&changed)
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.w().Begin()
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(`SELECT tool,turn_id,ordinal FROM tool_events WHERE state='started' ORDER BY started_ms`)
	if err != nil {
		tx.Rollback()
		return nil, err
	}
	var names []string
	type pending struct {
		tool, turn string
		ordinal    int
	}
	var pendingCalls []pending
	for rows.Next() {
		var t, turn string
		var ordinal int
		if err := rows.Scan(&t, &turn, &ordinal); err != nil {
			rows.Close()
			tx.Rollback()
			return nil, err
		}
		names = append(names, t)
		pendingCalls = append(pendingCalls, pending{t, turn, ordinal})
	}

	if err := rows.Err(); err != nil {
		rows.Close()
		tx.Rollback()
		return nil, fmt.Errorf("scan unfinished tool calls: %w", err)
	}
	rows.Close()
	if len(names) == 0 {
		tx.Rollback()
		return nil, nil
	}
	for _, p := range pendingCalls {
		if _, err := s.appendToolOutcome(tx, p.turn, p.ordinal, p.tool, "", "outcome unknown: runtime ended before a recorded completion", interaction.Unknown, false, ToolMeasurement{}); err != nil {
			tx.Rollback()
			return nil, err
		}
	}
	if _, err := tx.Exec(`UPDATE tool_events SET state='abandoned', finished_ms=? WHERE state='started'`,
		time.Now().UTC().UnixMilli()); err != nil {
		tx.Rollback()
		return nil, err
	}
	changed = true
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return names, nil
}

func (s *Store) ToolEventStats(window time.Duration) (done, failed, truncated int, err error) {
	cutoff := time.Now().UTC().Add(-window).UnixMilli()
	row := s.db.QueryRow(`SELECT COUNT(*), COALESCE(SUM(failed),0), COALESCE(SUM(truncated),0)
		FROM tool_events WHERE state='done' AND started_ms >= ?`, cutoff)
	if e := row.Scan(&done, &failed, &truncated); e != nil {
		if e == sql.ErrNoRows {
			return 0, 0, 0, nil
		}
		return 0, 0, 0, e
	}
	return done, failed, truncated, nil
}

type execer interface {
	Exec(query string, args ...any) (sql.Result, error)
}

func pruneToolEventsOn(db execer) (int64, error) {
	res, err := db.Exec(`DELETE FROM tool_events WHERE started_ms < ?`,
		time.Now().UTC().Add(-toolEventRetention).UnixMilli())
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

func (s *Store) PruneToolEvents() (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return pruneToolEventsOn(s.w())
}

func (s *Store) hasLegacyToolEvents() (bool, error) {
	return s.tableHasColumn("tool_events", "phase")
}

func InterruptedNote(names []string) string {
	if len(names) == 0 {
		return ""
	}
	return fmt.Sprintf("%d tool call(s) were in flight at the last shutdown (%s) — their side effects may or may not have completed; verify before repeating them.",
		len(names), strings.Join(names, ", "))
}

func (s *Store) StartedToolInteraction(turn string, ordinal int) (id, parent, model string, err error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	err = s.db.QueryRow(`SELECT e.execution_id,COALESCE(c.id,''),e.model FROM tool_events e LEFT JOIN conversations c ON c.source_kind='tool' AND c.source_id=e.execution_id AND c.kind='tool_call' WHERE e.turn_id=? AND e.ordinal=? AND e.state='started'`, turn, ordinal).Scan(&id, &parent, &model)
	return
}
