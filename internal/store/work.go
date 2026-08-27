package store

import (
	"database/sql"
	"strings"
	"time"
)

// --- Work Sessions ---

const subagentDescriptionPrefix = "sub-agent: "

func SubagentDescription(goal string) string { return subagentDescriptionPrefix + goal }

func SubagentGoal(description string) string {
	return strings.TrimPrefix(description, subagentDescriptionPrefix)
}

// WorkSession is a durable work tracking entry.
type WorkSession struct {
	ID          string
	Description string
	Project     string
	Status      string
	State       string // Ring 4 ephemeral working state
	LeaseOwner  string
	LeaseUntil  string
	CreatedSeq  uint64 // may be 0 — Ring 4 work sessions are not minted to ledger
	UpdatedSeq  uint64 // may be 0
	Result      string
}

// ActiveWorkSession returns the currently active work session, or nil.
func (s *Store) ActiveWorkSession() (*WorkSession, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var ws WorkSession
	var state, leaseOwner, leaseUntil, result sql.NullString
	var createdSeq, updatedSeq sql.NullInt64
	err := s.db.QueryRow(
		`SELECT id, description, status, state, lease_owner, lease_until, created_seq, updated_seq, result
		, project_id FROM work_sessions WHERE status = 'active' ORDER BY rowid DESC LIMIT 1`,
	).Scan(&ws.ID, &ws.Description, &ws.Status, &state, &leaseOwner, &leaseUntil, &createdSeq, &updatedSeq, &result, &ws.Project)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	ws.State = state.String
	ws.LeaseOwner = leaseOwner.String
	ws.LeaseUntil = leaseUntil.String
	ws.Result = result.String
	if createdSeq.Valid {
		ws.CreatedSeq = uint64(createdSeq.Int64)
	}
	if updatedSeq.Valid {
		ws.UpdatedSeq = uint64(updatedSeq.Int64)
	}
	return &ws, nil
}

// UpdateWorkState updates the ephemeral Ring 4 state of a work session.
func (s *Store) UpdateWorkState(sessionID, state string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.Exec(`UPDATE work_sessions SET state = ? WHERE id = ?`, state, sessionID)
	return err
}

// StartWorkSession inserts a new work session.
func (s *Store) StartWorkSession(id, description string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.Exec(
		`INSERT INTO work_sessions (id, description, status, state, project_id) VALUES (?, ?, 'active', '', ?)`,
		id, description, s.activeProject)
	return err
}

// SweepOrphanWorkSessions closes every work session left 'active' by a
// runtime that stopped before delivering it. Called once at boot: an
// orphaned 'active' row is a hand the identity no longer has, shown as
// live work forever. Closed as delivered with a result that says what
// actually happened — honest, queryable, no tombstone table.
func (s *Store) SweepOrphanWorkSessions() (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	res, err := s.db.Exec(`UPDATE work_sessions SET status='delivered', result='FAILED: interrupted by a runtime restart before delivery' WHERE status='active'`)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// DeliverWorkSession marks a work session as delivered with its result.
func (s *Store) DeliverWorkSession(id, result string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.Exec(`UPDATE work_sessions SET status='delivered', result=? WHERE id=?`,
		result, id)
	return err
}

// --- Identity Lifetime ---

// LifetimeTicks returns the current life-clock count.
func (s *Store) LifetimeTicks() (int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var ticks int64
	err := s.db.QueryRow(
		`SELECT lifetime_ticks FROM identity_lifetime WHERE singleton_id = 'current'`,
	).Scan(&ticks)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return ticks, err
}

// IncrementLifetimeTicks advances the life clock by 1 and records the tick time.
func (s *Store) IncrementLifetimeTicks() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.Exec(
		`UPDATE identity_lifetime SET lifetime_ticks = lifetime_ticks + 1, last_tick_at = ? WHERE singleton_id = 'current'`,
		time.Now().UTC().Format(time.RFC3339Nano),
	)
	return err
}

// RecentDeliveredSubagents returns the newest delivered sub-agent
// sessions (Ring 4, ephemeral — the identity reads outcomes here and
// NOTES what deserves to become memory; nothing mints automatically).
// LiveSubagentSessions lists sub-agent sessions currently running —
// the operator's window into live agency (the original spawn-invisible
// complaint, finally surfaced).
func (s *Store) LiveSubagentSessions() ([]WorkSession, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rows, err := s.db.Query(
		`SELECT id, description, status, COALESCE(state,''), COALESCE(result,'')
		, project_id FROM work_sessions
		 WHERE description LIKE ? AND status = 'active'
		 ORDER BY rowid DESC LIMIT 10`, subagentDescriptionPrefix+"%")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []WorkSession
	for rows.Next() {
		var w WorkSession
		if err := rows.Scan(&w.ID, &w.Description, &w.Status, &w.State, &w.Result, &w.Project); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

func (s *Store) RecentDeliveredSubagents(limit int) ([]WorkSession, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rows, err := s.db.Query(
		`SELECT id, description, status, COALESCE(state,''), COALESCE(result,'')
		, project_id FROM work_sessions
		 WHERE description LIKE ? AND status = 'delivered'
		 ORDER BY rowid DESC LIMIT ?`, subagentDescriptionPrefix+"%", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []WorkSession
	for rows.Next() {
		var w WorkSession
		if err := rows.Scan(&w.ID, &w.Description, &w.Status, &w.State, &w.Result, &w.Project); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// WorkSessionsByProject lists recent work sessions attributed to one
// project (project_id is stamped at session start). The Work tab's
// spine: sessions plus their owner-verdicted outcomes, by law (G1).
func (s *Store) WorkSessionsByProject(projectID string, limit int) ([]WorkSession, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rows, err := s.db.Query(
		`SELECT id, description, status, COALESCE(state,''), COALESCE(result,'')
		, project_id FROM work_sessions
		 WHERE project_id = ?
		 ORDER BY rowid DESC LIMIT ?`, projectID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []WorkSession
	for rows.Next() {
		var w WorkSession
		if err := rows.Scan(&w.ID, &w.Description, &w.Status, &w.State, &w.Result, &w.Project); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}
