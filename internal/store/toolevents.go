package store

import (
	"crypto/sha256"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .

const (
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	toolEventRetention = 14 * 24 * time.Hour
)

// .
func metaRecord(s string) string {
	return fmt.Sprintf("[%d chars, sha256=%x]", len([]rune(s)), sha256.Sum256([]byte(s)))
}

// .
// .
// .
// .
// .
type LegacyStart struct {
	TsMs   int64
	CallID string
	Tool   string
	Args   string
}

// .
// .
func (s *Store) RecordToolStart(turnID string, ordinal int, actor, model, providerCallID, tool, args string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`INSERT INTO tool_events
		(execution_id, turn_id, ordinal, actor, model, provider_call_id, tool, args_record, state, started_ms)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'started', ?)`,
		"tex_"+uuid.New().String(), turnID, ordinal, actor, model, providerCallID, tool,
		metaRecord(args), time.Now().UTC().UnixMilli())
	return err
}

// .
// .
// .
// .
// .
// .
// .
func (s *Store) RecordToolDone(turnID string, ordinal int, tool, args, result string, failed, truncated bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, tr := 0, 0
	if failed {
		f = 1
	}
	if truncated {
		tr = 1
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	res, err := tx.Exec(`UPDATE tool_events SET state='done', failed=?, truncated=?, result_record=?, finished_ms=?
		WHERE turn_id=? AND ordinal=? AND state='started'`,
		f, tr, metaRecord(result), time.Now().UTC().UnixMilli(), turnID, ordinal)
	if err != nil {
		tx.Rollback()
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		tx.Rollback()
		return fmt.Errorf("tool completion for turn %s ordinal %d matched %d started row(s), want exactly 1 — the execution record refuses to invent a completion", turnID, ordinal, n)
	}
	if err := s.recordToolExcerptTx(tx, tool, args, result); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

// .
// .
// .
// .
// .
// .
// .
func (s *Store) AbandonUnfinishedToolCalls() ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(`SELECT tool FROM tool_events WHERE state='started' ORDER BY started_ms`)
	if err != nil {
		tx.Rollback()
		return nil, err
	}
	var names []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			rows.Close()
			tx.Rollback()
			return nil, err
		}
		names = append(names, t)
	}
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
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
	if _, err := tx.Exec(`UPDATE tool_events SET state='abandoned', finished_ms=? WHERE state='started'`,
		time.Now().UTC().UnixMilli()); err != nil {
		tx.Rollback()
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return names, nil
}

// .
// .
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

// .
// .
// .
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

// .
func (s *Store) PruneToolEvents() (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return pruneToolEventsOn(s.db)
}

// .
// .
func (s *Store) hasLegacyToolEvents() (bool, error) {
	return s.tableHasColumn("tool_events", "phase")
}

// .
func InterruptedNote(names []string) string {
	if len(names) == 0 {
		return ""
	}
	return fmt.Sprintf("%d tool call(s) were in flight at the last shutdown (%s) — their side effects may or may not have completed; verify before repeating them.",
		len(names), strings.Join(names, ", "))
}
