package store

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// .

const subagentDescriptionPrefix = "sub-agent: "

func SubagentDescription(goal string) string { return subagentDescriptionPrefix + goal }

func SubagentGoal(description string) string {
	return strings.TrimPrefix(description, subagentDescriptionPrefix)
}

// .
type WorkSession struct {
	ID          string
	Description string
	Project     string
	Status      string
	State       string
	LeaseOwner  string
	LeaseUntil  string
	CreatedSeq  uint64
	UpdatedSeq  uint64
	Result      string
	HarvestedMs int64
	// .
	// .
	Focus    string
	NextMove string
	Plan     string
	// .
	// .
	ExpectedEvidence string
	Falsifier        string
	DecisionNeeded   string
	// .
	// .
	// .
	// .
	// .
	Evidence         string
	EvidenceReadback string
}

// .
// .
// .
// .
func (s *Store) WorkSessionByID(id string) (*WorkSession, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var ws WorkSession
	var state, leaseOwner, leaseUntil, result sql.NullString
	var createdSeq, updatedSeq sql.NullInt64
	err := s.db.QueryRow(
		`SELECT id, description, status, state, lease_owner, lease_until, created_seq, updated_seq, result
		, project_id, focus, next_move, plan, expected_evidence, falsifier, decision_needed, evidence, evidence_readback FROM work_sessions WHERE id = ?`, id,
	).Scan(&ws.ID, &ws.Description, &ws.Status, &state, &leaseOwner, &leaseUntil, &createdSeq, &updatedSeq, &result, &ws.Project, &ws.Focus, &ws.NextMove, &ws.Plan, &ws.ExpectedEvidence, &ws.Falsifier, &ws.DecisionNeeded, &ws.Evidence, &ws.EvidenceReadback)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	ws.State, ws.LeaseOwner, ws.LeaseUntil, ws.Result = state.String, leaseOwner.String, leaseUntil.String, result.String
	ws.CreatedSeq, ws.UpdatedSeq = uint64(createdSeq.Int64), uint64(updatedSeq.Int64)
	return &ws, nil
}

func (s *Store) ActiveWorkSession() (*WorkSession, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var ws WorkSession
	var state, leaseOwner, leaseUntil, result sql.NullString
	var createdSeq, updatedSeq sql.NullInt64
	err := s.db.QueryRow(
		`SELECT id, description, status, state, lease_owner, lease_until, created_seq, updated_seq, result
		, project_id, focus, next_move, plan, expected_evidence, falsifier, decision_needed, evidence, evidence_readback FROM work_sessions WHERE status = 'active' ORDER BY rowid DESC LIMIT 1`,
	).Scan(&ws.ID, &ws.Description, &ws.Status, &state, &leaseOwner, &leaseUntil, &createdSeq, &updatedSeq, &result, &ws.Project, &ws.Focus, &ws.NextMove, &ws.Plan, &ws.ExpectedEvidence, &ws.Falsifier, &ws.DecisionNeeded, &ws.Evidence, &ws.EvidenceReadback)
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

// .
func (s *Store) UpdateWorkState(sessionID, state string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.Exec(`UPDATE work_sessions SET state = ? WHERE id = ?`, state, sessionID)
	return err
}

// .
// .
// .
// .
// .
// .
func (s *Store) UpdateWorkPlan(sessionID string, focus, nextMove, plan, expectedEvidence, falsifier, decisionNeeded *string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if focus == nil && nextMove == nil && plan == nil && expectedEvidence == nil && falsifier == nil && decisionNeeded == nil {
		return nil
	}
	sets := make([]string, 0, 3)
	args := make([]interface{}, 0, 4)
	if focus != nil {
		sets = append(sets, "focus = ?")
		args = append(args, *focus)
	}
	if nextMove != nil {
		sets = append(sets, "next_move = ?")
		args = append(args, *nextMove)
	}
	if plan != nil {
		sets = append(sets, "plan = ?")
		args = append(args, *plan)
	}
	if expectedEvidence != nil {
		sets = append(sets, "expected_evidence = ?")
		args = append(args, *expectedEvidence)
	}
	if falsifier != nil {
		sets = append(sets, "falsifier = ?")
		args = append(args, *falsifier)
	}
	if decisionNeeded != nil {
		sets = append(sets, "decision_needed = ?")
		args = append(args, *decisionNeeded)
	}
	args = append(args, sessionID)
	q := `UPDATE work_sessions SET ` + strings.Join(sets, ", ") + ` WHERE id = ?`
	_, err := s.db.Exec(q, args...)
	return err
}

// .
func (s *Store) StartWorkSession(id, description string) error {
	var ev *WorkEvent
	defer s.notifyWork(&ev)
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.Exec(
		`INSERT INTO work_sessions (id, description, status, state, project_id) VALUES (?, ?, 'active', '', ?)`,
		id, description, s.activeProject)
	if err == nil {
		ev = &WorkEvent{Kind: WorkStarted, ID: id, Project: s.activeProject, Actor: workActor(description)}
	}
	return err
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
// .
// .
// .
// .
// .
// .
// .
// .
// .
func (s *Store) RecentPlan() (*WorkSession, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var ws WorkSession
	var state, leaseOwner, leaseUntil, result sql.NullString
	var createdSeq, updatedSeq sql.NullInt64
	err := s.db.QueryRow(
		`SELECT id, description, status, state, lease_owner, lease_until, created_seq, updated_seq, result
		, project_id, focus, next_move, plan, expected_evidence, falsifier, decision_needed, evidence, evidence_readback FROM work_sessions
		WHERE (focus != '' OR next_move != '' OR plan != '')
		AND description NOT LIKE ?
		ORDER BY rowid DESC LIMIT 1`,
		subagentDescriptionPrefix+"%").Scan(&ws.ID, &ws.Description, &ws.Status, &state, &leaseOwner, &leaseUntil, &createdSeq, &updatedSeq, &result, &ws.Project, &ws.Focus, &ws.NextMove, &ws.Plan, &ws.ExpectedEvidence, &ws.Falsifier, &ws.DecisionNeeded, &ws.Evidence, &ws.EvidenceReadback)
	if err == sql.ErrNoRows {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
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
	return &ws, true, nil
}

func (s *Store) SweepOrphanWorkSessions() (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	res, err := s.db.Exec(`UPDATE work_sessions SET status='delivered', result='FAILED: interrupted by a runtime restart before delivery', evidence='external_effect_unknown', delivered_at=? WHERE status='active'`, time.Now().UTC().UnixMilli())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// .
// .
// .
// .
// .
func (s *Store) DeliverWorkSession(id, result, evidence, readback string) error {
	if evidence != "" && !IsEvidenceClass(evidence) {
		return fmt.Errorf("deliver: unknown evidence class %q", evidence)
	}
	if EvidenceVerifiedTier(evidence) && evidenceReadbackClean(readback) == "" {
		return fmt.Errorf("deliver: evidence class %q requires a verification readback", evidence)
	}
	if EvidenceVerifiedTier(evidence) && !strings.HasPrefix(result, "served:") {
		return fmt.Errorf("deliver: a %s result must begin served: — a verified class cannot attach to a partial or unserved outcome", evidence)
	}
	var ev *WorkEvent
	defer s.notifyWork(&ev)
	s.mu.Lock()
	defer s.mu.Unlock()

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
	res, err := s.db.Exec(`UPDATE work_sessions SET status='delivered', result=?, evidence=?, evidence_readback=?, delivered_at=?, harvested_ms=NULL
		WHERE id=? AND (status != 'delivered' OR result LIKE 'unserved: failed before start:%')`,
		result, evidence, readback, time.Now().UTC().UnixMilli(), id)
	if err != nil {
		return err
	}
	if n, aerr := res.RowsAffected(); aerr == nil && n == 0 {
		return fmt.Errorf("delivery refused for %s: session already delivered with a real result (trajectory replay?)", id)
	}
	project, description := s.workRow(id)
	ev = &WorkEvent{Kind: WorkDelivered, ID: id, Project: project, Actor: workActor(description), Outcome: outcomeClass(result), Evidence: evidence}
	return nil
}

// .

// .
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

// .
func (s *Store) IncrementLifetimeTicks() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.Exec(
		`UPDATE identity_lifetime SET lifetime_ticks = lifetime_ticks + 1, last_tick_at = ? WHERE singleton_id = 'current'`,
		time.Now().UTC().Format(time.RFC3339Nano),
	)
	return err
}

// .
// .
// .
// .
// .
// .
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

// .
// .
// .
// .
// .
// .
// .
// .
func (s *Store) UnharvestedDeliveries(limit int) ([]WorkSession, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rows, err := s.db.Query(
		`SELECT id, description, status, COALESCE(state,''), COALESCE(result,'')
		, project_id, COALESCE(evidence,''), COALESCE(harvested_ms,0) FROM work_sessions
		 WHERE description LIKE ? AND status = 'delivered' AND harvested_ms IS NULL
		 ORDER BY rowid ASC LIMIT ?`, subagentDescriptionPrefix+"%", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []WorkSession
	for rows.Next() {
		var w WorkSession
		if err := rows.Scan(&w.ID, &w.Description, &w.Status, &w.State, &w.Result, &w.Project, &w.Evidence, &w.HarvestedMs); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// .
// .
// .
// .
func (s *Store) MarkHarvested(id string, ms int64) error {
	var ev *WorkEvent
	defer s.notifyWork(&ev)
	s.mu.Lock()
	defer s.mu.Unlock()
	res, err := s.db.Exec(`UPDATE work_sessions SET harvested_ms=? WHERE id=?`, ms, id)
	if err == nil {
		if n, aerr := res.RowsAffected(); aerr == nil && n > 0 {
			project, description := s.workRow(id)
			ev = &WorkEvent{Kind: WorkHarvested, ID: id, Project: project, Actor: workActor(description)}
		}
	}
	return err
}

func (s *Store) RecentDeliveredSubagents(limit int) ([]WorkSession, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rows, err := s.db.Query(
		`SELECT id, description, status, COALESCE(state,''), COALESCE(result,'')
		, project_id, COALESCE(evidence,'') FROM work_sessions
		 WHERE description LIKE ? AND status = 'delivered'
		 ORDER BY rowid DESC LIMIT ?`, subagentDescriptionPrefix+"%", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []WorkSession
	for rows.Next() {
		var w WorkSession
		if err := rows.Scan(&w.ID, &w.Description, &w.Status, &w.State, &w.Result, &w.Project, &w.Evidence); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// .
// .
// .
// .
// .
// .
// .
// .
func (s *Store) WorkSessionsByProject(projectID string, limit int) ([]WorkSession, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rows, err := s.db.Query(
		`SELECT id, description, status, COALESCE(state,''), COALESCE(result,'')
		, project_id FROM work_sessions
		 WHERE project_id = ?
		 ORDER BY rowid DESC LIMIT ?+1`, projectID, limit)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	var out []WorkSession
	for rows.Next() {
		var w WorkSession
		if err := rows.Scan(&w.ID, &w.Description, &w.Status, &w.State, &w.Result, &w.Project); err != nil {
			return nil, false, err
		}
		out = append(out, w)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	// .
	// .
	if len(out) > limit {
		return out[:limit], true, nil
	}
	return out, false, nil
}

// .

// .
// .
// .
// .
// .
type ResumeFields struct {
	Focus            string
	Established      string
	NextAction       string
	ExpectedEvidence string
	Falsifier        string
	DecisionNeeded   string
	ResumePoint      string
	Evidence         string
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
func RenderResumeCard(f ResumeFields) string {
	if f.Focus == "" && f.Established == "" && f.NextAction == "" && f.ResumePoint == "" {
		return ""
	}
	orUnknown := func(v string) string {
		if strings.TrimSpace(v) == "" {
			return "not specified"
		}
		return v
	}
	var b strings.Builder
	b.WriteString("### Resume — the one next thing, and what would confirm it")
	if f.Focus != "" {
		b.WriteString("\nFocus: " + f.Focus)
	}
	if f.Established != "" {
		b.WriteString("\nEstablished: " + f.Established)
	}
	if f.NextAction != "" {
		b.WriteString("\nNext action: " + f.NextAction)
	}
	b.WriteString("\nExpected evidence: " + orUnknown(f.ExpectedEvidence))
	b.WriteString("\nFalsifier: " + orUnknown(f.Falsifier))
	if f.DecisionNeeded != "" {
		b.WriteString("\nDecision needed: " + f.DecisionNeeded)
	}
	if f.Evidence != "" {
		b.WriteString("\nEvidence: " + EvidenceScopeLabel(f.Evidence))
		if rec := EvidenceRecovery(f.Evidence); rec != "" {
			b.WriteString("\nRecovery: " + rec)
		}
	}
	if f.ResumePoint != "" {
		b.WriteString("\nResume point: " + f.ResumePoint)
	}
	return b.String()
}
