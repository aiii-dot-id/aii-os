package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/interaction"
	"strings"
	"time"
)

const openToolCall = `c.kind='tool_call' AND c.source_kind='tool' AND NOT EXISTS (
	SELECT 1 FROM conversations r WHERE r.source_kind='tool' AND r.source_id=c.source_id AND r.kind='tool_result')`

const openCallsSinceSQL = `SELECT c.turn_id, json_extract(c.details,'$.emission_ordinal'), json_extract(c.details,'$.tool')
	FROM conversations c WHERE c.turn_seq >= ? AND c.recorded_at >= ? AND ` + openToolCall + ` ORDER BY c.turn_seq`

const windowFloorSQL = `SELECT MIN(turn_seq) FROM conversations INDEXED BY idx_conversations_recorded_seq WHERE recorded_at >= ?`

const unfinishedWindow = 14 * 24 * time.Hour

func toolExecutionID(turnID string, ordinal int) string {
	return fmt.Sprintf("tex_%x", sha256.Sum256([]byte(fmt.Sprintf("%s/%d", turnID, ordinal))))
}

func metaRecord(s string) string {
	return fmt.Sprintf("[%d chars, sha256=%x]", len([]rune(s)), sha256.Sum256([]byte(s)))
}

func windowSince(q interface {
	QueryRow(string, ...any) *sql.Row
}, cutoff time.Time) (floor int64, stamp string, ok bool, err error) {
	stamp, err = interaction.NormalizeTime(cutoff)
	if err != nil {
		return 0, "", false, err
	}
	var min sql.NullInt64
	if err := q.QueryRow(windowFloorSQL, stamp).Scan(&min); err != nil {
		return 0, "", false, err
	}
	return min.Int64, stamp, min.Valid, nil
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
	id := toolExecutionID(turnID, ordinal)
	project, work, why := s.activeProject, "", ""
	switch actor {
	case "operator":
		why = "operator_act"
	case "main", "safe":
		err = tx.QueryRow(`SELECT id FROM work_sessions WHERE status='active' AND NOT `+queuedChild("")+` ORDER BY rowid DESC LIMIT 1`, SubagentWorkKind, SubagentWorkKind).Scan(&work)
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
	if !added {
		return fmt.Errorf("tool start for turn %s ordinal %d is already recorded", turnID, ordinal)
	}
	err = tx.Commit()
	changed = added
	return err

}

func (s *Store) ToolCallStarted(turnID string, ordinal int) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM conversations c WHERE c.source_kind='tool' AND c.source_id=? AND `+openToolCall,
		toolExecutionID(turnID, ordinal)).Scan(&n); err != nil {
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
	defer tx.Rollback()
	floor, stamp, found, err := windowSince(tx, time.Now().Add(-unfinishedWindow))
	if err != nil || !found {
		return nil, err
	}
	rows, err := tx.Query(openCallsSinceSQL, floor, stamp)
	if err != nil {
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
		var ordinal sql.NullInt64
		if err := rows.Scan(&turn, &ordinal, &t); err != nil {
			rows.Close()
			return nil, err
		}
		names = append(names, t)
		pendingCalls = append(pendingCalls, pending{t, turn, int(ordinal.Int64)})
	}

	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("scan unfinished tool calls: %w", err)
	}
	rows.Close()
	if len(names) == 0 {
		return nil, nil
	}
	for _, p := range pendingCalls {
		if _, err := s.appendToolOutcome(tx, p.turn, p.ordinal, p.tool, "", "outcome unknown: runtime ended before a recorded completion", interaction.Unknown, false, ToolMeasurement{}); err != nil {
			return nil, err
		}
	}
	changed = true
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return names, nil
}

const toolResultStatsSQL = `SELECT COUNT(*), COALESCE(SUM(outcome='failed'),0), COALESCE(SUM(json_extract(details,'$.truncated')),0)
	FROM conversations WHERE kind='tool_result' AND turn_seq >= ? AND recorded_at >= ? AND outcome IN ('succeeded','failed')`

func (s *Store) ToolResultStats(window time.Duration) (done, failed, truncated int, err error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return toolResultStatsSince(s.db, time.Now().Add(-window))
}

func toolResultStatsSince(q interface {
	QueryRow(string, ...any) *sql.Row
}, cutoff time.Time) (done, failed, truncated int, err error) {
	floor, stamp, found, err := windowSince(q, cutoff)
	if err != nil || !found {
		return 0, 0, 0, err
	}
	err = q.QueryRow(toolResultStatsSQL, floor, stamp).Scan(&done, &failed, &truncated)
	return done, failed, truncated, err
}

type heldToolStart struct {
	turn, actor, model, call, tool, args string
	ordinal                              int
}

func (s *Store) heldToolStarts() ([]heldToolStart, error) {
	if ok, err := s.tableHasColumn("tool_events", "state"); err != nil || !ok {
		return nil, err
	}
	rows, err := s.h().Query(`SELECT turn_id,ordinal,actor,model,provider_call_id,tool,args_record FROM tool_events WHERE state='started' ORDER BY started_ms`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var held []heldToolStart
	for rows.Next() {
		var h heldToolStart
		if err := rows.Scan(&h.turn, &h.ordinal, &h.actor, &h.model, &h.call, &h.tool, &h.args); err != nil {
			return nil, err
		}
		held = append(held, h)
	}
	return held, rows.Err()
}

func (s *Store) carryToolStarts(held []heldToolStart) (string, error) {
	if len(held) == 0 {
		return "", nil
	}
	var before int64
	if err := s.h().QueryRow(`SELECT COUNT(*) FROM conversations`).Scan(&before); err != nil {
		return "", err
	}
	carried := 0
	for _, h := range held {
		id := toolExecutionID(h.turn, h.ordinal)
		var n int
		if err := s.h().QueryRow(`SELECT COUNT(*) FROM conversations WHERE source_kind='tool' AND source_id=? AND kind='tool_call'`, id).Scan(&n); err != nil {
			return "", err
		}
		if n > 0 {
			continue
		}
		details, err := json.Marshal(interaction.Details{Tool: h.tool, Actor: h.actor, Model: h.model, ProviderCallID: h.call, Ordinal: h.ordinal, ArgsRecord: h.args, LegacySource: "tool_events", OrderBasis: "import"})
		if err != nil {
			return "", err
		}
		stamp, err := interaction.NormalizeTime(time.Now())
		if err != nil {
			return "", err
		}

		if _, err := s.h().Exec(`INSERT INTO conversations(id,session_id,role,content,turn_seq,created_at,kind,turn_id,source_kind,source_id,recorded_at,details) VALUES(?,'default','system',?,COALESCE((SELECT MAX(turn_seq) FROM conversations),0)+1,?,'tool_call',?,'tool',?,?,?)`,
			"tool_call_"+id, fmt.Sprintf("→ %s(%s)", h.tool, h.args), stamp, h.turn, id, stamp, string(details)); err != nil {
			return "", err
		}
		carried++
	}
	if carried == 0 {
		return "", nil
	}
	var after int64
	if err := s.h().QueryRow(`SELECT COUNT(*) FROM conversations`).Scan(&after); err != nil {
		return "", err
	}
	if s.schemaConversions == nil {
		s.schemaConversions = map[string]RuntimeConversion{}
	}
	reason := "unfinished tool executions carried from the retired tool_events"
	if previous, ok := s.schemaConversions["conversations"]; ok {
		before = previous.Before
		reason = previous.Reason + "; " + reason
	}
	s.schemaConversions["conversations"] = RuntimeConversion{Before: before, After: after, Reason: reason}
	return fmt.Sprintf("RECONCILE: %d unfinished tool call(s) carried from tool_events into the conversation log for the boot warning", carried), nil
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
	err = s.db.QueryRow(`SELECT c.source_id, c.id, COALESCE(json_extract(c.details,'$.model'),'') FROM conversations c WHERE c.source_kind='tool' AND c.source_id=? AND `+openToolCall,
		toolExecutionID(turn, ordinal)).Scan(&id, &parent, &model)
	return
}
